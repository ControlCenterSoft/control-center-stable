package migrations

import (
	"context"
	"strings"
	"testing"
	"time"

	postgresstore "control-center/internal/persistence/postgres"
)

// stable030Up is the exact SQL upgrade boundary shipped by the canonical
// Control Center v0.30.0 Public Stable release. 0011_job_timeline is the first
// schema addition on the committed 0.31 line and must never be folded back
// into this baseline.
var stable030Up = []string{
	"0001_initial.up.sql",
	"0002_local_identity_rbac_audit.up.sql",
	"0003_identity_persistence_invariants.up.sql",
	"0004_change_execution_core.up.sql",
	"0005_first_login_password_change.up.sql",
	"0006_distributed_core_objects.up.sql",
	"0007_network_contract_objects.up.sql",
	"0008_builtin_rbac_permissions.up.sql",
	"0009_auth_session_activity.up.sql",
	"0010_legacy_03_schema_compatibility.up.sql",
}

// TestPostgresUpgradeFrom030PreservesCredentialsAndDurableJob proves the
// concrete Stable 0.30 -> committed 0.31 schema transition rather than relying
// only on the older 0.3 compatibility fixture. It deliberately seeds operator
// credentials and an already-existing durable Job before applying 0011.
//
// The qualification is fail-closed around the properties that matter for a
// safe product upgrade:
//   - the administrator-selected password and first-login state are preserved;
//   - the existing Change/Job identity, state, version and payload digest are
//     not rewritten;
//   - 0011 records only a bounded `observed` baseline timeline event for the
//     pre-existing Job instead of inventing historical transitions;
//   - replay, scoped rollback, re-apply and database reconnect remain safe.
func TestPostgresUpgradeFrom030PreservesCredentialsAndDurableJob(t *testing.T) {
	database := newDisposablePostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	assertProductSchemaAbsent(t, ctx, database.db)
	applyMigrations(t, ctx, database.db, stable030Up)

	const (
		bootstrapHash = "$argon2id$v=19$qualification-030-bootstrap"
		selectedHash  = "$argon2id$v=19$qualification-030-selected"
		changeID      = "stable-030-change"
		jobID         = "stable-030-job"
	)
	seededAt := time.Date(2026, 9, 12, 3, 30, 0, 123000000, time.UTC)

	adminID, created, err := postgresstore.BootstrapAdmin(ctx, database.db, "admin", bootstrapHash, seededAt.Add(-2*time.Minute))
	if err != nil || !created || adminID == "" {
		t.Fatalf("bootstrap Stable 0.30 admin created=%v has_id=%v err=%v", created, adminID != "", err)
	}
	identities, err := postgresstore.NewIdentityStore(database.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := identities.ChangePasswordAndRevokeSessions(ctx, adminID, bootstrapHash, selectedHash, seededAt.Add(-time.Minute)); err != nil {
		t.Fatalf("select non-bootstrap admin password: %v", err)
	}

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO cc_config_revisions
(id,sequence,digest,content,created_at,created_by)
VALUES ('stable-030-revision',30,$1,'{"release":"0.30.0","desired":"unchanged"}',$2,$3)`,
		"sha256:"+strings.Repeat("3", 64), seededAt.Add(-45*time.Second), adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cc_policy_decisions
(id,policy_id,effect,risk,reason,minimum_approvals,distinct_actors,prohibit_requester,evaluated_at)
VALUES ('stable-030-policy-decision','stable-030-policy','allow','low','qualified Stable 0.30 fixture',0,true,false,$1)`, seededAt.Add(-40*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cc_changes
(id,action_name,requester,revision_id,decision_id,risk,input,idempotency_key,input_fingerprint,job_id,state,version,created_at,updated_at)
VALUES ($1,'resource.record',$2,'stable-030-revision','stable-030-policy-decision','low',
 '{"resourceId":"node-stable-030"}','stable-030-change-key',$3,$4,'queued',4,$5,$5)`,
		changeID, adminID, strings.Repeat("c", 64), jobID, seededAt.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cc_jobs
(id,change_id,action_name,input,idempotency_key,input_fingerprint,status,attempt,max_attempts,version,created_at,updated_at)
VALUES ($1,$2,'resource.record','{"resourceId":"node-stable-030"}','stable-030-job-key',$3,'queued',0,3,7,$4,$4)`,
		jobID, changeID, strings.Repeat("d", 64), seededAt); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	assertStable030UpgradeFixture(t, ctx, database, adminID, selectedHash, changeID, jobID, seededAt, false)

	applyMigrations(t, ctx, database.db, []string{"0011_job_timeline.up.sql"})
	assertCurrentSchema(t, ctx, database.db)
	assertStable030UpgradeFixture(t, ctx, database, adminID, selectedHash, changeID, jobID, seededAt, true)

	// Replaying 0011 is required to be idempotent: it may not duplicate the
	// baseline observation or mutate the pre-existing Job.
	applyMigrations(t, ctx, database.db, []string{"0011_job_timeline.up.sql"})
	assertStable030UpgradeFixture(t, ctx, database, adminID, selectedHash, changeID, jobID, seededAt, true)

	// Roll back only the 0.31-owned schema. Stable 0.30 state must remain exact.
	applyMigrations(t, ctx, database.db, []string{"0011_job_timeline.down.sql"})
	assertStable030UpgradeFixture(t, ctx, database, adminID, selectedHash, changeID, jobID, seededAt, false)

	applyMigrations(t, ctx, database.db, []string{"0011_job_timeline.up.sql"})
	database.restart(t, ctx)
	assertCurrentSchema(t, ctx, database.db)
	assertStable030UpgradeFixture(t, ctx, database, adminID, selectedHash, changeID, jobID, seededAt, true)
}

func assertStable030UpgradeFixture(
	t *testing.T,
	ctx context.Context,
	database *disposablePostgres,
	adminID string,
	selectedHash string,
	changeID string,
	jobID string,
	seededAt time.Time,
	wantTimeline bool,
) {
	t.Helper()

	var storedHash string
	var passwordChangeRequired bool
	if err := database.db.QueryRowContext(ctx, `SELECT password_hash,password_change_required
FROM cc_local_users WHERE id=$1::uuid`, adminID).Scan(&storedHash, &passwordChangeRequired); err != nil {
		t.Fatal(err)
	}
	if storedHash != selectedHash || passwordChangeRequired {
		t.Fatal("Stable 0.30 administrator password or first-login state changed during upgrade")
	}

	var (
		storedChangeID    string
		storedAction      string
		storedFingerprint string
		storedStatus      string
		storedAttempt     int
		storedMaxAttempts int
		storedVersion     uint64
		storedUpdatedAt   time.Time
	)
	if err := database.db.QueryRowContext(ctx, `SELECT change_id,action_name,input_fingerprint,status,attempt,max_attempts,version,updated_at
FROM cc_jobs WHERE id=$1`, jobID).Scan(
		&storedChangeID, &storedAction, &storedFingerprint, &storedStatus,
		&storedAttempt, &storedMaxAttempts, &storedVersion, &storedUpdatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if storedChangeID != changeID || storedAction != "resource.record" || storedFingerprint != strings.Repeat("d", 64) ||
		storedStatus != "queued" || storedAttempt != 0 || storedMaxAttempts != 3 || storedVersion != 7 || !storedUpdatedAt.Equal(seededAt) {
		t.Fatalf("Stable 0.30 durable Job changed during upgrade: change=%q action=%q status=%q attempt=%d max=%d version=%d updated=%s",
			storedChangeID, storedAction, storedStatus, storedAttempt, storedMaxAttempts, storedVersion, storedUpdatedAt)
	}

	var timelineExists bool
	if err := database.db.QueryRowContext(ctx, `SELECT to_regclass('public.cc_job_timeline') IS NOT NULL`).Scan(&timelineExists); err != nil {
		t.Fatal(err)
	}
	if timelineExists != wantTimeline {
		t.Fatalf("timeline table exists=%v, want %v", timelineExists, wantTimeline)
	}
	if !wantTimeline {
		return
	}

	var (
		event      string
		status     string
		attempt    int
		jobVersion uint64
		occurredAt time.Time
		count      int
	)
	if err := database.db.QueryRowContext(ctx, `SELECT event,status,attempt,job_version,occurred_at
FROM cc_job_timeline WHERE job_id=$1`, jobID).Scan(&event, &status, &attempt, &jobVersion, &occurredAt); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT count(*) FROM cc_job_timeline WHERE job_id=$1`, jobID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || event != "observed" || status != "queued" || attempt != 0 || jobVersion != 7 || !occurredAt.Equal(seededAt) {
		t.Fatalf("Stable 0.30 baseline timeline evidence invalid: count=%d event=%q status=%q attempt=%d version=%d occurred=%s",
			count, event, status, attempt, jobVersion, occurredAt)
	}
}
