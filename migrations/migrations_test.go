package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
	postgresstore "control-center/internal/persistence/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	migrationTestDatabaseEnv    = "MIGRATION_TEST_DATABASE_URL"
	migrationInstallDatabaseEnv = "MIGRATION_INSTALL_DATABASE_URL"
)

var (
	migrationNamePattern = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.(up|down)\.sql$`)
	supported03Up        = []string{
		"0001_initial.up.sql",
		"0002_local_identity_rbac_audit.up.sql",
		"0003_identity_persistence_invariants.up.sql",
		"0004_change_execution_core.up.sql",
		"0005_first_login_password_change.up.sql",
	}
)

func TestMigrationOrdinalsAreUniqueAndEvery04MigrationIsReversible(t *testing.T) {
	upFiles := migrationFiles(t, "*.up.sql")
	if len(upFiles) < len(supported03Up)+2 {
		t.Fatalf("current migration set has %d up migrations, want at least %d", len(upFiles), len(supported03Up)+2)
	}
	seenOrdinals := make(map[int]string, len(upFiles))
	upByStem := make(map[string]struct{}, len(upFiles))
	for _, name := range upFiles {
		ordinal := migrationOrdinal(t, name)
		if previous, duplicate := seenOrdinals[ordinal]; duplicate {
			t.Fatalf("migration ordinal %04d is reused by %s and %s", ordinal, previous, name)
		}
		seenOrdinals[ordinal] = name
		upByStem[strings.TrimSuffix(name, ".up.sql")] = struct{}{}
	}
	for _, name := range supported03Up {
		if _, exists := upByStem[strings.TrimSuffix(name, ".up.sql")]; !exists {
			t.Errorf("supported 0.3 migration %s is missing", name)
		}
	}

	downByStem := make(map[string]struct{})
	for _, name := range migrationFiles(t, "*.down.sql") {
		stem := strings.TrimSuffix(name, ".down.sql")
		downByStem[stem] = struct{}{}
		if _, exists := upByStem[stem]; !exists {
			t.Errorf("down migration %s has no matching up migration", name)
		}
	}
	for _, name := range upFiles {
		if migrationOrdinal(t, name) < 6 {
			continue
		}
		stem := strings.TrimSuffix(name, ".up.sql")
		if _, exists := downByStem[stem]; !exists {
			t.Errorf("0.4 migration %s has no scoped down migration", name)
		}
	}
}

func TestDistributedCoreUpgradeMigrationIsAdditiveAndDeterministic(t *testing.T) {
	up := readMigration(t, "0006_distributed_core_objects.up.sql")
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS cc_core_objects",
		"CREATE TABLE IF NOT EXISTS cc_core_object_mutations",
		"resource_version varchar(255) NOT NULL UNIQUE",
		"idempotency_key varchar(255) PRIMARY KEY",
		"request_fingerprint char(64) NOT NULL",
		"cc_core_object_mutations_resource_version_format",
		"jsonb_typeof(document) = 'object'",
		"'global', 'scope', 'global', 'global', 1",
		"'bootstrap-v0.3-global'",
		"ON CONFLICT (object_id) DO NOTHING",
		"'core.objects.read'",
		"'core.objects.write'",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("0006 up migration lacks %q", required)
		}
	}
	upper := strings.ToUpper(up)
	for _, forbidden := range []string{
		"DROP TABLE ORGANIZATIONS",
		"DROP TABLE RESOURCES",
		"DROP TABLE CONFIG_REVISIONS",
		"ALTER TABLE ORGANIZATIONS",
		"ALTER TABLE RESOURCES",
		"ALTER TABLE CONFIG_REVISIONS",
	} {
		if strings.Contains(upper, forbidden) {
			t.Fatalf("0006 upgrade is not additive: found %q", forbidden)
		}
	}
}

func TestDistributedCoreDownMigrationIsScoped(t *testing.T) {
	down := strings.ToUpper(readMigration(t, "0006_distributed_core_objects.down.sql"))
	if !strings.Contains(down, "DROP TABLE IF EXISTS CC_CORE_OBJECT_MUTATIONS") {
		t.Fatal("0006 down migration does not remove its receipt table")
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS CC_CORE_OBJECTS") {
		t.Fatal("0006 down migration does not remove its table")
	}
	for _, legacy := range []string{"ORGANIZATIONS", "RESOURCES", "CONFIG_REVISIONS", "CC_LOCAL_USERS", "CC_AUDIT_EVENTS"} {
		if strings.Contains(down, "DROP TABLE "+legacy) || strings.Contains(down, "ALTER TABLE "+legacy) {
			t.Fatalf("0006 down migration touches legacy table %s", legacy)
		}
	}
}

func TestNetworkContractMigrationIsAdditiveAndFailsClosedOnDowngrade(t *testing.T) {
	up := readMigration(t, "0007_network_contract_objects.up.sql")
	for _, required := range []string{
		"ALTER TABLE cc_core_objects",
		"'network-zone'",
		"'network-interface'",
		"ADD CONSTRAINT cc_core_objects_type CHECK",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("0007 up migration lacks %q", required)
		}
	}
	upper := strings.ToUpper(up)
	for _, legacy := range []string{"ORGANIZATIONS", "RESOURCES", "CONFIG_REVISIONS"} {
		if strings.Contains(upper, "ALTER TABLE "+legacy) || strings.Contains(upper, "DROP TABLE "+legacy) {
			t.Fatalf("0007 upgrade touches legacy table %s", legacy)
		}
	}

	down := readMigration(t, "0007_network_contract_objects.down.sql")
	for _, required := range []string{
		"WHERE object_type IN ('network-zone', 'network-interface')",
		"RAISE EXCEPTION 'cannot downgrade while network contract objects exist'",
		"ADD CONSTRAINT cc_core_objects_type CHECK",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("0007 down migration lacks %q", required)
		}
	}
}

func TestBuiltinRBACMigrationMatchesCanonicalRegistry(t *testing.T) {
	up := readMigration(t, "0008_builtin_rbac_permissions.up.sql")
	for _, definition := range rbac.BuiltinPermissions() {
		row := fmt.Sprintf("('%s', '%s')", sqlLiteral(string(definition.Name)), sqlLiteral(definition.Description))
		if !strings.Contains(up, row) {
			t.Errorf("0008 lacks canonical permission row for %q", definition.Name)
		}
	}
	for _, role := range rbac.BuiltinRoles() {
		for _, permission := range role.Permissions {
			row := fmt.Sprintf("('%s', '%s')", sqlLiteral(role.Name), sqlLiteral(string(permission)))
			if !strings.Contains(up, row) {
				t.Errorf("0008 lacks canonical grant %s -> %s", role.Name, permission)
			}
		}
	}
	down := strings.ToUpper(readMigration(t, "0008_builtin_rbac_permissions.down.sql"))
	if !strings.Contains(down, "CC_MIGRATION_0008_RBAC_SEED") || !strings.Contains(down, "NOT EXISTS") {
		t.Fatal("0008 down migration does not use its conservative insertion ledger")
	}
	if strings.Contains(down, "CASCADE") {
		t.Fatal("0008 down migration may cascade into custom RBAC state")
	}
}

func TestPostgresCleanInstallAndRestart(t *testing.T) {
	database := newDisposablePostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	assertProductSchemaAbsent(t, ctx, database.db)
	applyMigrations(t, ctx, database.db, migrationFiles(t, "*.up.sql"))
	assertCurrentSchema(t, ctx, database.db)
	assertBuiltinRBACParity(t, ctx, database.db, true)
	firstCore := readCoreBootstrap(t, ctx, database.db)

	// The 0.4 migrations are replay-safe. Reapplying them must neither
	// duplicate grants nor rewrite the deterministic core bootstrap object.
	applyMigrations(t, ctx, database.db, current04Migrations(t, true))
	assertCurrentSchema(t, ctx, database.db)
	assertBuiltinRBACParity(t, ctx, database.db, true)
	assertSameCoreBootstrap(t, firstCore, readCoreBootstrap(t, ctx, database.db))

	const firstHash = "$argon2id$v=19$qualification-clean-initial"
	const replacementHash = "$argon2id$v=19$qualification-clean-replacement"
	adminID, created, err := postgresstore.BootstrapAdmin(ctx, database.db, "admin", firstHash, time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC))
	if err != nil || !created || adminID == "" {
		t.Fatalf("clean-install admin bootstrap created=%v has_id=%v err=%v", created, adminID != "", err)
	}
	if _, createdAgain, err := postgresstore.BootstrapAdmin(ctx, database.db, "admin", replacementHash, time.Date(2026, 9, 9, 10, 1, 0, 0, time.UTC)); err != nil || createdAgain {
		t.Fatalf("replayed admin bootstrap created=%v err=%v", createdAgain, err)
	}
	assertPasswordHashUnchanged(t, ctx, database.db, adminID, firstHash)

	database.restart(t, ctx)
	assertCurrentSchema(t, ctx, database.db)
	assertBuiltinRBACParity(t, ctx, database.db, true)
	assertSameCoreBootstrap(t, firstCore, readCoreBootstrap(t, ctx, database.db))
	assertPasswordHashUnchanged(t, ctx, database.db, adminID, firstHash)
}

func TestPostgresUpgradeFrom03PreservesLegacyState(t *testing.T) {
	database := newDisposablePostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	assertProductSchemaAbsent(t, ctx, database.db)
	applyMigrations(t, ctx, database.db, supported03Up)
	legacy := seedLegacy03State(t, ctx, database.db)
	assertLegacy03State(t, ctx, database.db, legacy)

	applyMigrations(t, ctx, database.db, current04Migrations(t, true))
	assertCurrentSchema(t, ctx, database.db)
	assertBuiltinRBACParity(t, ctx, database.db, false)
	assertLegacy03State(t, ctx, database.db, legacy)
	firstCore := readCoreBootstrap(t, ctx, database.db)
	assertUpgradeBootstrapDoesNotReplacePassword(t, ctx, database.db, legacy)
	seedPostUpgradeCustomGrant(t, ctx, database.db)
	assertPostUpgradeCustomGrant(t, ctx, database.db)

	applyMigrations(t, ctx, database.db, current04Migrations(t, true))
	assertLegacy03State(t, ctx, database.db, legacy)
	assertSameCoreBootstrap(t, firstCore, readCoreBootstrap(t, ctx, database.db))
	assertPostUpgradeCustomGrant(t, ctx, database.db)

	// Roll back only the 0.4 layer. All 0.3 identity, RBAC, audit,
	// inventory, configuration, and durable Change state must remain exact.
	applyMigrations(t, ctx, database.db, current04Migrations(t, false))
	assert04SchemaAbsent(t, ctx, database.db)
	assertLegacy03State(t, ctx, database.db, legacy)
	assertPostUpgradeCustomGrant(t, ctx, database.db)

	applyMigrations(t, ctx, database.db, current04Migrations(t, true))
	assertCurrentSchema(t, ctx, database.db)
	assertBuiltinRBACParity(t, ctx, database.db, false)
	assertLegacy03State(t, ctx, database.db, legacy)
	assertUpgradeBootstrapDoesNotReplacePassword(t, ctx, database.db, legacy)
	assertPostUpgradeCustomGrant(t, ctx, database.db)

	database.restart(t, ctx)
	assertCurrentSchema(t, ctx, database.db)
	assertLegacy03State(t, ctx, database.db, legacy)
	assertUpgradeBootstrapDoesNotReplacePassword(t, ctx, database.db, legacy)
	assertPostUpgradeCustomGrant(t, ctx, database.db)
	assertAuditIsAppendOnly(t, ctx, database.db, legacy.auditEventID)
}

// TestPostgresInstallCurrentSchemaFixture is an explicitly gated CI setup
// step for adapter/race tests. It refuses a non-empty database and is skipped
// unless the caller supplies a dedicated disposable service database.
func TestPostgresInstallCurrentSchemaFixture(t *testing.T) {
	databaseURL := os.Getenv(migrationInstallDatabaseEnv)
	if databaseURL == "" {
		t.Skip(migrationInstallDatabaseEnv + " is not set; schema fixture skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal("open explicitly selected schema fixture database")
	}
	// Close only the client connection. The installed schema deliberately
	// remains for the following adapter/race step in the disposable CI service.
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal("connect to explicitly selected schema fixture database")
	}
	assertProductSchemaAbsent(t, ctx, database)
	applyMigrations(t, ctx, database, migrationFiles(t, "*.up.sql"))
	assertCurrentSchema(t, ctx, database)
	assertBuiltinRBACParity(t, ctx, database, true)
}

type disposablePostgres struct {
	base         *sql.DB
	db           *sql.DB
	databaseURL  string
	databaseName string
}

func newDisposablePostgres(t *testing.T) *disposablePostgres {
	t.Helper()
	baseURL := os.Getenv(migrationTestDatabaseEnv)
	if baseURL == "" {
		t.Skip(migrationTestDatabaseEnv + " is not set; PostgreSQL qualification skipped")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		t.Fatal(migrationTestDatabaseEnv + " must be a valid PostgreSQL URL")
	}
	base, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal("open PostgreSQL qualification control connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := base.PingContext(ctx); err != nil {
		base.Close()
		t.Fatal("connect to PostgreSQL qualification control database")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", t.Name(), time.Now().UnixNano())))
	databaseName := "ccq_" + hex.EncodeToString(sum[:10])
	if _, err := base.ExecContext(ctx, "CREATE DATABASE "+quoteIdentifier(databaseName)+" TEMPLATE template0"); err != nil {
		base.Close()
		t.Fatalf("create isolated qualification database: %v", err)
	}
	parsed.Path = "/" + databaseName
	query := parsed.Query()
	query.Set("application_name", "control-center-upgrade-qualification")
	parsed.RawQuery = query.Encode()
	isolatedURL := parsed.String()
	database, err := sql.Open("pgx", isolatedURL)
	if err != nil {
		_, _ = base.ExecContext(ctx, "DROP DATABASE "+quoteIdentifier(databaseName)+" WITH (FORCE)")
		base.Close()
		t.Fatal("open isolated qualification database")
	}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		_, _ = base.ExecContext(ctx, "DROP DATABASE "+quoteIdentifier(databaseName)+" WITH (FORCE)")
		base.Close()
		t.Fatal("connect to isolated qualification database")
	}
	result := &disposablePostgres{base: base, db: database, databaseURL: isolatedURL, databaseName: databaseName}
	t.Cleanup(func() {
		if result.db != nil {
			_ = result.db.Close()
		}
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := result.base.ExecContext(cleanupContext, "DROP DATABASE "+quoteIdentifier(result.databaseName)+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated qualification database: %v", err)
		}
		_ = result.base.Close()
	})
	return result
}

func (d *disposablePostgres) restart(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := d.db.Close(); err != nil {
		t.Fatalf("close qualification database for restart: %v", err)
	}
	d.db = nil
	database, err := sql.Open("pgx", d.databaseURL)
	if err != nil {
		t.Fatal("reopen qualification database")
	}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		t.Fatal("reconnect to qualification database after restart")
	}
	d.db = database
}

type legacy03Fixture struct {
	organizationID  string
	nodeID          string
	marketID        string
	adminID         string
	selectedHash    string
	passwordChanged time.Time
	sessionDigest   string
	auditEventID    string
	auditHash       string
}

func seedLegacy03State(t *testing.T, ctx context.Context, database *sql.DB) legacy03Fixture {
	t.Helper()
	fixture := legacy03Fixture{
		organizationID:  "00000000-0000-4000-8000-000000000301",
		nodeID:          "00000000-0000-4000-8000-000000000302",
		marketID:        "00000000-0000-4000-8000-000000000303",
		selectedHash:    "$argon2id$v=19$qualification-selected-password",
		passwordChanged: time.Date(2026, 9, 8, 12, 5, 0, 123000000, time.UTC),
		sessionDigest:   strings.Repeat("d", 64),
		auditEventID:    "00000000-0000-4000-8000-000000000304",
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO organizations (id,slug,name)
VALUES ($1,'legacy-03','Legacy 0.3 organization')`, fixture.organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO resources
(id,organization_id,kind,name,status,labels,specification,observed_state)
VALUES
($1,$3,'node','Legacy node','ready','{"site":"legacy"}',
 '{"contract_version":"agent.enrollment/v1","node_id":"node-legacy","hostname":"legacy.example.test","capabilities":["inventory","pxe"]}',
 '{"health":"ok"}'),
($2,$3,'market-manifest','Legacy market module','ready','{"source":"builtin-v1"}',
 '{"id":"legacy-module","version":"1.2.3","capabilities":["inventory"],"platforms":["linux"],"lifecycle":["install","upgrade","remove"]}',
 '{"status":"installed"}')`, fixture.nodeID, fixture.marketID, fixture.organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO config_revisions
(organization_id,resource_id,revision,configuration,checksum_sha256,created_by)
VALUES
($1,$2,1,'{"enabled":true}',$4,'legacy-operator'),
($1,$3,1,'{"channel":"stable"}',$5,'legacy-operator')`, fixture.organizationID, fixture.nodeID, fixture.marketID, strings.Repeat("a", 64), strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}

	const bootstrapHash = "$argon2id$v=19$qualification-bootstrap-password"
	adminID, created, err := postgresstore.BootstrapAdmin(ctx, database, "admin", bootstrapHash, fixture.passwordChanged.Add(-time.Minute))
	if err != nil || !created {
		t.Fatalf("seed 0.3 administrator created=%v err=%v", created, err)
	}
	fixture.adminID = adminID
	identities, err := postgresstore.NewIdentityStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := identities.ChangePasswordAndRevokeSessions(ctx, fixture.adminID, bootstrapHash, fixture.selectedHash, fixture.passwordChanged); err != nil {
		t.Fatal("select non-bootstrap administrator password")
	}
	session := auth.Session{
		ID: "00000000-0000-4000-8000-000000000305", UserID: fixture.adminID,
		TokenDigest: fixture.sessionDigest, CredentialVersion: fixture.passwordChanged,
		CreatedAt: fixture.passwordChanged.Add(time.Minute), ExpiresAt: fixture.passwordChanged.Add(2 * time.Hour),
		SourceIP: "127.0.0.1", UserAgent: "upgrade-qualification",
	}
	// Seed the legacy session with the v0.3 schema directly. Using the current
	// identity adapter here would make the fixture depend on post-0.3 columns.
	if _, err := database.ExecContext(ctx, `INSERT INTO cc_auth_sessions
(id,user_id,token_digest,credential_version,created_at,expires_at,source_ip,user_agent)
VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,NULLIF($7,'')::inet,$8)`,
		session.ID, session.UserID, session.TokenDigest, session.CredentialVersion.UTC(),
		session.CreatedAt.UTC(), session.ExpiresAt.UTC(), session.SourceIP, session.UserAgent,
	); err != nil {
		t.Fatalf("seed 0.3 administrator session: %v", err)
	}

	// This same-named future permission and its custom/built-in grants prove
	// that 0008 uses additive ON CONFLICT behavior and a conservative down path.
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO cc_rbac_roles (name,description,built_in) VALUES ('legacy-extension','Legacy extension role',false)`, nil},
		{`INSERT INTO cc_rbac_permissions (name,description) VALUES
 ('market.manifests.read','Legacy extension-owned definition'),
 ('legacy.custom.read','Legacy custom permission')`, nil},
		{`INSERT INTO cc_rbac_role_permissions (role_name,permission_name) VALUES
 ('legacy-extension','market.manifests.read'),
 ('legacy-extension','legacy.custom.read'),
 ('viewer','market.manifests.read')`, nil},
		{`INSERT INTO cc_rbac_user_bindings (id,user_id,role_name,scope_kind,scope_id,created_at,created_by)
VALUES ('00000000-0000-4000-8000-000000000306',$1::uuid,'legacy-extension','global',NULL,$2,$1::uuid)`, []any{fixture.adminID, fixture.passwordChanged}},
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	auditLog, err := postgresstore.NewAuditLog(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditLog.Append(ctx, audit.Event{
		ID: fixture.auditEventID, OccurredAt: fixture.passwordChanged.Add(2 * time.Minute),
		Action: "upgrade.fixture.created", Outcome: "success", ActorID: fixture.adminID,
		Details: map[string]any{"source_version": "0.3"},
	}); err != nil {
		t.Fatal("seed 0.3 audit event")
	}
	if err := database.QueryRowContext(ctx, `SELECT hash FROM cc_audit_events WHERE id=$1::uuid`, fixture.auditEventID).Scan(&fixture.auditHash); err != nil {
		t.Fatal(err)
	}

	changeTransaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer changeTransaction.Rollback()
	if _, err := changeTransaction.ExecContext(ctx, `INSERT INTO cc_config_revisions (id,sequence,digest,content,created_at,created_by)
VALUES ('legacy-revision',1,$1,'{"generation":3}',$2,$3)`, "sha256:"+strings.Repeat("c", 64), fixture.passwordChanged, fixture.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := changeTransaction.ExecContext(ctx, `INSERT INTO cc_policy_decisions (id,policy_id,effect,risk,reason,minimum_approvals,distinct_actors,prohibit_requester,evaluated_at)
VALUES ('legacy-decision','legacy-policy','allow','high','approved under 0.3 policy',1,true,true,$1)`, fixture.passwordChanged); err != nil {
		t.Fatal(err)
	}
	if _, err := changeTransaction.ExecContext(ctx, `INSERT INTO cc_changes (id,action_name,requester,revision_id,decision_id,risk,input,idempotency_key,input_fingerprint,state,version,created_at,updated_at)
VALUES ('legacy-change','resource.record',$1,'legacy-revision','legacy-decision','high','{"resourceId":"node-legacy"}',
 'legacy-change-key',$2,'pending_approval',3,$3,$3)`, fixture.adminID, strings.Repeat("e", 64), fixture.passwordChanged); err != nil {
		t.Fatal(err)
	}
	if err := changeTransaction.Commit(); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertLegacy03State(t *testing.T, ctx context.Context, database *sql.DB, fixture legacy03Fixture) {
	t.Helper()
	var inventoryValid bool
	if err := database.QueryRowContext(ctx, `SELECT count(*)=2
 AND count(*) FILTER (WHERE id=$2::uuid AND kind='node' AND status='ready'
   AND labels='{"site":"legacy"}'::jsonb
   AND specification->>'contract_version'='agent.enrollment/v1'
   AND specification->>'node_id'='node-legacy'
   AND observed_state='{"health":"ok"}'::jsonb)=1
 AND count(*) FILTER (WHERE id=$3::uuid AND kind='market-manifest' AND status='ready'
   AND labels='{"source":"builtin-v1"}'::jsonb
   AND specification->>'id'='legacy-module'
   AND specification->>'version'='1.2.3'
   AND observed_state='{"status":"installed"}'::jsonb)=1
FROM resources WHERE organization_id=$1::uuid`, fixture.organizationID, fixture.nodeID, fixture.marketID).Scan(&inventoryValid); err != nil {
		t.Fatal(err)
	}
	if !inventoryValid {
		t.Fatal("legacy Node or Market resource changed during migration")
	}
	var revisionCount int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM config_revisions
WHERE organization_id=$1::uuid AND resource_id IN ($2::uuid,$3::uuid)`, fixture.organizationID, fixture.nodeID, fixture.marketID).Scan(&revisionCount); err != nil || revisionCount != 2 {
		t.Fatalf("legacy configuration revision count=%d err=%v, want 2", revisionCount, err)
	}

	var storedHash string
	var passwordChangeRequired bool
	var passwordChanged time.Time
	if err := database.QueryRowContext(ctx, `SELECT password_hash,password_change_required,password_changed_at
FROM cc_local_users WHERE id=$1::uuid`, fixture.adminID).Scan(&storedHash, &passwordChangeRequired, &passwordChanged); err != nil {
		t.Fatal(err)
	}
	if storedHash != fixture.selectedHash || passwordChangeRequired || !passwordChanged.Equal(fixture.passwordChanged) {
		t.Fatal("administrator credential or password-change state changed during migration")
	}
	var sessionValid bool
	if err := database.QueryRowContext(ctx, `SELECT count(*)=1 AND bool_and(user_id=$2::uuid)
 AND bool_and(credential_version=$3) AND bool_and(revoked_at IS NULL)
FROM cc_auth_sessions WHERE token_digest=$1`, fixture.sessionDigest, fixture.adminID, fixture.passwordChanged).Scan(&sessionValid); err != nil || !sessionValid {
		t.Fatalf("administrator session was not retained: valid=%v err=%v", sessionValid, err)
	}

	var legacyDescription string
	if err := database.QueryRowContext(ctx, `SELECT description FROM cc_rbac_permissions WHERE name='market.manifests.read'`).Scan(&legacyDescription); err != nil {
		t.Fatal(err)
	}
	if legacyDescription != "Legacy extension-owned definition" {
		t.Fatal("upgrade replaced a pre-existing permission definition")
	}
	var retainedGrants int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM cc_rbac_role_permissions
WHERE (role_name='legacy-extension' AND permission_name IN ('market.manifests.read','legacy.custom.read'))
   OR (role_name='viewer' AND permission_name='market.manifests.read')`).Scan(&retainedGrants); err != nil || retainedGrants != 3 {
		t.Fatalf("pre-existing RBAC grants retained=%d err=%v, want 3", retainedGrants, err)
	}
	authorizer := postgresstore.NewAuthorizer(database)
	if !authorizer.Allowed(fixture.adminID, rbac.Permission("legacy.custom.read"), rbac.GlobalScope()) ||
		!authorizer.Allowed(fixture.adminID, rbac.PermissionMarketRead, rbac.GlobalScope()) ||
		!authorizer.Allowed(fixture.adminID, rbac.PermissionRolesWrite, rbac.GlobalScope()) {
		t.Fatal("retained administrator or custom RBAC binding does not authorize its original permissions")
	}

	auditLog, err := postgresstore.NewAuditLog(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditLog.VerifyChain(ctx); err != nil {
		t.Fatalf("retained audit chain failed verification: %v", err)
	}
	var auditHash string
	if err := database.QueryRowContext(ctx, `SELECT hash FROM cc_audit_events WHERE id=$1::uuid`, fixture.auditEventID).Scan(&auditHash); err != nil || auditHash != fixture.auditHash {
		t.Fatal("audit event identity or hash changed during migration")
	}

	var durableChangeValid bool
	if err := database.QueryRowContext(ctx, `SELECT count(*)=1 AND bool_and(revision_id='legacy-revision')
 AND bool_and(decision_id='legacy-decision') AND bool_and(version=3)
FROM cc_changes WHERE id='legacy-change'`).Scan(&durableChangeValid); err != nil || !durableChangeValid {
		t.Fatalf("0.3 durable Change state was not retained: valid=%v err=%v", durableChangeValid, err)
	}
}

func assertUpgradeBootstrapDoesNotReplacePassword(t *testing.T, ctx context.Context, database *sql.DB, fixture legacy03Fixture) {
	t.Helper()
	const releaseBootstrapHash = "$argon2id$v=19$qualification-release-bootstrap"
	adminID, created, err := postgresstore.BootstrapAdmin(ctx, database, "admin", releaseBootstrapHash, fixture.passwordChanged.Add(time.Hour))
	if err != nil || created || adminID != fixture.adminID {
		t.Fatalf("upgrade bootstrap identity_match=%v created=%v err=%v", adminID == fixture.adminID, created, err)
	}
	assertPasswordHashUnchanged(t, ctx, database, fixture.adminID, fixture.selectedHash)
}

func seedPostUpgradeCustomGrant(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `INSERT INTO cc_rbac_role_permissions (role_name,permission_name)
VALUES ('legacy-extension','nodes.lifecycle.plan')`); err != nil {
		t.Fatal("add custom grant to a permission introduced by 0008")
	}
}

func assertPostUpgradeCustomGrant(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var retained bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM cc_rbac_role_permissions
 WHERE role_name='legacy-extension' AND permission_name='nodes.lifecycle.plan'
)`).Scan(&retained); err != nil || !retained {
		t.Fatalf("custom grant on a built-in permission retained=%v err=%v", retained, err)
	}
}

func assertPasswordHashUnchanged(t *testing.T, ctx context.Context, database *sql.DB, userID, expectedHash string) {
	t.Helper()
	var actualHash string
	if err := database.QueryRowContext(ctx, `SELECT password_hash FROM cc_local_users WHERE id=$1::uuid`, userID).Scan(&actualHash); err != nil {
		t.Fatal(err)
	}
	if actualHash != expectedHash {
		t.Fatal("administrator password hash changed")
	}
}

func assertAuditIsAppendOnly(t *testing.T, ctx context.Context, database *sql.DB, eventID string) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `UPDATE cc_audit_events SET outcome='tampered' WHERE id=$1::uuid`, eventID); err == nil {
		t.Fatal("append-only audit event accepted an update after upgrade restart")
	}
}

type coreBootstrap struct {
	objectType      string
	scopeID         string
	ownerScope      string
	generation      int64
	resourceVersion string
	document        string
	documentValid   bool
	createdAt       time.Time
	updatedAt       time.Time
}

func readCoreBootstrap(t *testing.T, ctx context.Context, database *sql.DB) coreBootstrap {
	t.Helper()
	var result coreBootstrap
	if err := database.QueryRowContext(ctx, `SELECT object_type,scope_id,owner_scope,generation,
resource_version,document::text,document='{"id":"global","kind":"global","name":"Global"}'::jsonb,
created_at,updated_at FROM cc_core_objects WHERE object_id='global'`).Scan(
		&result.objectType, &result.scopeID, &result.ownerScope, &result.generation,
		&result.resourceVersion, &result.document, &result.documentValid, &result.createdAt, &result.updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if result.objectType != "scope" || result.scopeID != "global" || result.ownerScope != "global" ||
		result.generation != 1 || result.resourceVersion != "bootstrap-v0.3-global" ||
		!result.documentValid || !result.createdAt.Equal(result.updatedAt) {
		t.Fatal("distributed core bootstrap object is missing or non-canonical")
	}
	return result
}

func assertSameCoreBootstrap(t *testing.T, before, after coreBootstrap) {
	t.Helper()
	if before != after {
		t.Fatal("replayed migration or adapter restart rewrote the core bootstrap object")
	}
}

func assertCurrentSchema(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"organizations", "resources", "config_revisions",
		"cc_local_users", "cc_auth_sessions", "cc_rbac_roles", "cc_rbac_permissions",
		"cc_rbac_role_permissions", "cc_rbac_user_bindings", "cc_audit_events",
		"cc_config_revisions", "cc_idempotency_keys", "cc_policy_decisions", "cc_changes",
		"cc_change_approvals", "cc_jobs", "cc_actual_states", "cc_health_observations",
		"cc_core_objects", "cc_core_object_mutations", "cc_migration_0008_rbac_seed",
	} {
		var present bool
		if err := database.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&present); err != nil || !present {
			t.Errorf("current schema table %s present=%v err=%v", table, present, err)
		}
	}
	var columnsPresent bool
	if err := database.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='cc_local_users' AND column_name='password_change_required')
 AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='cc_auth_sessions' AND column_name='credential_version')`).Scan(&columnsPresent); err != nil || !columnsPresent {
		t.Errorf("current identity schema columns present=%v err=%v", columnsPresent, err)
	}
	var triggerCount int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM pg_trigger
WHERE NOT tgisinternal AND tgname IN ('cc_audit_events_no_update','cc_audit_events_no_delete','cc_config_revisions_immutable')`).Scan(&triggerCount); err != nil || triggerCount != 3 {
		t.Errorf("immutable/audit trigger count=%d err=%v, want 3", triggerCount, err)
	}
	readCoreBootstrap(t, ctx, database)
	assertNetworkObjectTypesEnabled(t, ctx, database)
}

func assertBuiltinRBACParity(t *testing.T, ctx context.Context, database *sql.DB, requireCanonicalDescriptions bool) {
	t.Helper()
	for _, definition := range rbac.BuiltinPermissions() {
		var description string
		if err := database.QueryRowContext(ctx, `SELECT description FROM cc_rbac_permissions WHERE name=$1`, string(definition.Name)).Scan(&description); err != nil {
			t.Errorf("built-in permission %s is absent: %v", definition.Name, err)
			continue
		}
		if requireCanonicalDescriptions && description != definition.Description {
			t.Errorf("built-in permission %s description drifted", definition.Name)
		}
	}
	for _, role := range rbac.BuiltinRoles() {
		for _, permission := range role.Permissions {
			var present bool
			if err := database.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM cc_rbac_role_permissions WHERE role_name=$1 AND permission_name=$2
)`, role.Name, string(permission)).Scan(&present); err != nil || !present {
				t.Errorf("built-in RBAC grant %s -> %s present=%v err=%v", role.Name, permission, present, err)
			}
		}
	}
}

func assertProductSchemaAbsent(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var occupied bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1
 FROM pg_class AS relation
 JOIN pg_namespace AS namespace ON namespace.oid=relation.relnamespace
 WHERE namespace.nspname=current_schema()
   AND (relation.relname IN ('organizations','resources','config_revisions')
        OR relation.relname LIKE 'cc\_%' ESCAPE '\')
)`).Scan(&occupied); err != nil {
		t.Fatal(err)
	}
	if occupied {
		t.Fatal("qualification database is not empty; refusing to modify it")
	}
}

func assert04SchemaAbsent(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var absent bool
	if err := database.QueryRowContext(ctx, `SELECT
 to_regclass('cc_core_objects') IS NULL AND
 to_regclass('cc_core_object_mutations') IS NULL AND
 to_regclass('cc_migration_0008_rbac_seed') IS NULL`).Scan(&absent); err != nil || !absent {
		t.Fatalf("0.4 schema remains after scoped down migration: absent=%v err=%v", absent, err)
	}
	for _, permission := range []string{"core.objects.read", "core.objects.write", "nodes.enrollment.plan"} {
		var present bool
		if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cc_rbac_permissions WHERE name=$1)`, permission).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present {
			t.Errorf("0.4 permission %s remains after scoped down migration", permission)
		}
	}
}

func applyMigrations(t *testing.T, ctx context.Context, database *sql.DB, names []string) {
	t.Helper()
	for _, name := range names {
		applyMigration(t, ctx, database, name)
	}
}

func applyMigration(t *testing.T, ctx context.Context, database *sql.DB, name string) {
	t.Helper()
	content := readMigration(t, name)
	if _, err := database.ExecContext(ctx, content); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

func current04Migrations(t *testing.T, up bool) []string {
	t.Helper()
	pattern := "*.up.sql"
	if !up {
		pattern = "*.down.sql"
	}
	files := migrationFiles(t, pattern)
	result := make([]string, 0, len(files))
	for _, name := range files {
		if migrationOrdinal(t, name) >= 6 {
			result = append(result, name)
		}
	}
	if !up {
		sort.Sort(sort.Reverse(sort.StringSlice(result)))
	}
	return result
}

func migrationFiles(t *testing.T, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	for index := range files {
		files[index] = filepath.Base(files[index])
	}
	sort.Strings(files)
	return files
}

func migrationOrdinal(t *testing.T, name string) int {
	t.Helper()
	match := migrationNamePattern.FindStringSubmatch(filepath.Base(name))
	if match == nil {
		t.Fatalf("migration file %q does not use NNNN_name.direction.sql", name)
	}
	ordinal, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return ordinal
}

func assertNetworkObjectTypesEnabled(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var definition string
	if err := database.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid='cc_core_objects'::regclass AND conname='cc_core_objects_type'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "network-zone") || !strings.Contains(definition, "network-interface") {
		t.Fatalf("network object types missing from PostgreSQL constraint: %s", definition)
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func sqlLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
