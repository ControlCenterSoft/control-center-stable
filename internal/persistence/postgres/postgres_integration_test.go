package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
	"control-center/internal/identity/security"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/events"
	orchestrationapi "control-center/internal/orchestration/httpapi"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

func TestPostgresStateSurvivesAdapterRestart(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var migrationPresent bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cc_jobs') IS NOT NULL
        AND to_regclass('cc_core_objects') IS NOT NULL
        AND to_regclass('cc_core_object_mutations') IS NOT NULL
        AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='cc_local_users' AND column_name='password_change_required')
        AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='cc_auth_sessions' AND column_name='credential_version')`).Scan(&migrationPresent); err != nil || !migrationPresent {
		t.Fatalf("database must be migrated through 0006 before integration tests: present=%v err=%v", migrationPresent, err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash("integration-test-password-12345")
	if err != nil {
		t.Fatal(err)
	}
	username := "integration-" + suffix
	userID, created, err := BootstrapAdmin(ctx, db, username, passwordHash, time.Now().UTC())
	if err != nil || !created {
		t.Fatalf("initial bootstrap created=%v err=%v", created, err)
	}
	differentHash, err := hasher.Hash("a-different-integration-password")
	if err != nil {
		t.Fatal(err)
	}
	secondID, createdAgain, err := BootstrapAdmin(ctx, db, username, differentHash, time.Now().UTC())
	if err != nil || createdAgain || secondID != userID {
		t.Fatalf("idempotent bootstrap id=%q second=%q created=%v err=%v", userID, secondID, createdAgain, err)
	}
	identities, _ := NewIdentityStore(db)
	bootstrappedUser, err := identities.FindUserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrappedUser.PasswordHash != passwordHash || !bootstrappedUser.PasswordChangeRequired {
		t.Fatal("repeat bootstrap replaced credentials or removed first-login requirement")
	}
	passwordChangedAt := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if err := identities.ChangePasswordAndRevokeSessions(ctx, userID, passwordHash, differentHash, passwordChangedAt); err != nil {
		t.Fatal(err)
	}
	if _, createdAfterChange, err := BootstrapAdmin(ctx, db, username, passwordHash, time.Now().UTC()); err != nil || createdAfterChange {
		t.Fatalf("post-change bootstrap created=%v err=%v", createdAfterChange, err)
	}
	preservedUser, err := identities.FindUserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if preservedUser.PasswordHash != differentHash || preservedUser.PasswordChangeRequired || !preservedUser.PasswordChangedAt.Equal(passwordChangedAt) {
		t.Fatal("restart bootstrap reset the selected password or its state")
	}
	if !NewAuthorizer(db).Allowed(userID, rbac.PermissionChangesWrite, rbac.GlobalScope()) {
		t.Fatal("persisted administrator binding did not authorize")
	}
	session := auth.Session{ID: deterministicUUID("session:" + suffix), UserID: userID, TokenDigest: hexDigest("token:" + suffix), CredentialVersion: passwordChangedAt, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), SourceIP: "127.0.0.1", UserAgent: "integration-test"}
	if err := identities.CreateSession(ctx, session, differentHash); err != nil {
		t.Fatal(err)
	}
	restartedIdentities, _ := NewIdentityStore(db)
	if restored, err := restartedIdentities.FindSessionByDigest(ctx, session.TokenDigest); err != nil || restored.ID != session.ID {
		t.Fatalf("session after restart=%#v err=%v", restored, err)
	}
	auditLog, _ := NewAuditLog(db)
	if err := auditLog.Append(ctx, audit.Event{Action: "integration.restart", Outcome: "success", ActorID: userID}); err != nil {
		t.Fatal(err)
	}

	state, _ := NewOrchestrationState(db)
	content := json.RawMessage(`{"generation":1}`)
	fingerprint := hexDigestBytes(content)
	revision, err := state.CreateRevision(ctx, userID, "revision-"+suffix, fingerprint, content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Decision{Effect: policy.EffectAllow, Risk: policy.RiskHigh, Reason: "integration test", PolicyID: "integration-v1", Requirement: policy.ApprovalRequirement{Minimum: 1, Permission: string(rbac.PermissionChangesApprove), DistinctActors: true}}
	changeID := "chg-integration-" + suffix
	persisted := orchestrationapi.PersistedChange{Snapshot: change.Snapshot{ID: changeID, Action: "resource.record", Requester: userID, RevisionID: revision.ID, Risk: policy.RiskHigh, State: change.StatePendingApproval, Decision: decision, Version: 1, UpdatedAt: time.Now().UTC()}, Input: json.RawMessage(`{"resourceId":"node-1","kind":"node","state":"present"}`), IdempotencyKey: "change-" + suffix, Fingerprint: hexDigest("change-input:" + suffix)}
	if _, created, err := state.CreateChange(ctx, persisted); err != nil || !created {
		t.Fatalf("create change created=%v err=%v", created, err)
	}
	persisted.Snapshot.State = change.StateApproved
	persisted.Snapshot.Version = 2
	persisted.Snapshot.Approvals = []policy.Approval{{Actor: userID, Permissions: []string{string(rbac.PermissionChangesApprove)}, ApprovedAt: time.Now().UTC()}}
	jobs, _ := NewJobRepository(db)
	createdJob, _, err := jobs.Create(ctx, job.CreateRequest{ID: "job-integration-" + suffix, ChangeID: changeID, ActionName: "resource.record", Input: persisted.Input, IdempotencyKey: "job-" + suffix, MaxAttempts: 2, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	persisted.JobID = createdJob.ID
	persisted.Snapshot.State = change.StateQueued
	persisted.Snapshot.Version = 3
	if err := state.UpdateChange(ctx, persisted); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := jobs.Claim(ctx, "integration-worker", time.Now().UTC(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	output := events.Output{ActualStates: []events.ActualState{{ResourceID: "node-1", Kind: "node", State: events.StatePresent, ObservedAt: time.Now().UTC()}}}
	if _, err := jobs.Succeed(ctx, claimed.ID, claimed.Lease.Token, output, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	restartedState, _ := NewOrchestrationState(db)
	loaded, err := restartedState.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundChange := false
	for _, candidate := range loaded.Changes {
		if candidate.Snapshot.ID == changeID {
			foundChange = candidate.JobID == createdJob.ID && len(candidate.Snapshot.Approvals) == 1
		}
	}
	if !foundChange {
		t.Fatal("change, approval, or job binding was lost across adapter restart")
	}
	restartedJobs, _ := NewJobRepository(db)
	if restored, err := restartedJobs.Get(ctx, createdJob.ID); err != nil || restored.Status != job.StatusSucceeded || restored.Output == nil {
		t.Fatalf("job after restart=%#v err=%v", restored, err)
	}
	_, err = orchestrationapi.New(orchestrationapi.Config{Registry: action.NewRegistry(), Jobs: restartedJobs, Persistence: restartedState, Evaluator: policy.ThresholdEvaluator{PolicyID: "integration-v1", ApprovalPermission: string(rbac.PermissionChangesApprove)}, Protect: func(_ rbac.Permission, next http.Handler) http.Handler { return next }, Actor: func(*http.Request) (string, bool) { return userID, true }, Middleware: func(next http.Handler) http.Handler { return next }, Context: ctx})
	if err != nil {
		t.Fatalf("startup terminal job reconciliation: %v", err)
	}
	repaired, err := restartedState.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundRepairedChange := false
	for _, candidate := range repaired.Changes {
		if candidate.Snapshot.ID == changeID {
			foundRepairedChange = candidate.Snapshot.State == change.StateSucceeded
		}
	}
	if !foundRepairedChange {
		t.Fatal("startup reconciliation did not repair the terminal job's durable change state")
	}
}

func TestPostgresCoreObjectRepositoryCASAndRestart(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var schemaReady bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cc_core_objects') IS NOT NULL AND to_regclass('cc_core_object_mutations') IS NOT NULL`).Scan(&schemaReady); err != nil || !schemaReady {
		t.Fatalf("database must be migrated through 0006: ready=%v err=%v", schemaReady, err)
	}

	repository, err := NewCoreObjectRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	objectID := "desired-integration-" + suffix
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM cc_core_object_mutations WHERE object_id=$1`, objectID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM cc_core_objects WHERE object_id=$1`, objectID)
	})
	create := corecontracts.MutationRequest{
		Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectDesiredState,
		ObjectID: objectID, ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"kind":"integration.config","target_object_id":"service-integration","spec":{"generation":1}}`),
	}
	created, err := repository.Apply(ctx, create, "core-create-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if created.Generation != 1 || created.ResourceVersion == "" {
		t.Fatalf("created metadata=%#v", created.ObjectMetadata)
	}

	restarted, err := NewCoreObjectRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Get(ctx, objectID)
	if err != nil || loaded.ResourceVersion != created.ResourceVersion {
		t.Fatalf("object after adapter restart=%#v err=%v", loaded, err)
	}
	replayed, err := restarted.Apply(ctx, create, "core-create-"+suffix)
	if err != nil || replayed.ResourceVersion != created.ResourceVersion || !replayed.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("idempotent replay=%#v err=%v", replayed, err)
	}

	generation := created.Generation
	replace := corecontracts.MutationRequest{
		Operation: corecontracts.MutationReplace, ObjectType: corecontracts.ObjectDesiredState,
		ObjectID: objectID, ScopeID: "global", OwnerScope: "global",
		Document:     json.RawMessage(`{"kind":"integration.config","target_object_id":"service-integration","spec":{"generation":2}}`),
		Precondition: &corecontracts.ObjectPrecondition{ObjectID: objectID, ResourceVersion: created.ResourceVersion, Generation: &generation},
	}
	updated, err := restarted.Apply(ctx, replace, "core-replace-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 2 || updated.ResourceVersion == created.ResourceVersion {
		t.Fatalf("updated metadata=%#v", updated.ObjectMetadata)
	}
	if _, err := restarted.Apply(ctx, replace, "core-stale-"+suffix); !errors.Is(err, corecontracts.ErrPreconditionFailed) {
		t.Fatalf("stale CAS error=%v", err)
	}
	create.ObjectID = "another-object-" + suffix
	if _, err := restarted.Apply(ctx, create, "core-create-"+suffix); !errors.Is(err, corecontracts.ErrIdempotencyConflict) {
		t.Fatalf("idempotency key conflict error=%v", err)
	}

	objects, err := restarted.List(ctx, corecontracts.ObjectFilter{ObjectType: corecontracts.ObjectDesiredState, ScopeID: "global"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, object := range objects {
		found = found || object.ObjectID == objectID && object.ResourceVersion == updated.ResourceVersion
	}
	if !found {
		t.Fatalf("updated object absent from filtered list: %#v", objects)
	}
}

func TestPostgresNetworkContractsSurviveAdapterRestart(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var networkTypesEnabled bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid='cc_core_objects'::regclass
      AND conname='cc_core_objects_type'
      AND pg_get_constraintdef(oid) LIKE '%network-zone%'
      AND pg_get_constraintdef(oid) LIKE '%network-interface%'
)`).Scan(&networkTypesEnabled); err != nil || !networkTypesEnabled {
		t.Fatalf("database must be migrated through 0007: enabled=%v err=%v", networkTypesEnabled, err)
	}

	repository, err := NewCoreObjectRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	scopeID := "network-scope-" + suffix
	siteID := "network-site-" + suffix
	roleID := "network-agent-role-" + suffix
	zoneID := "network-zone-" + suffix
	interfaceID := "network-interface-" + suffix
	objectIDs := []string{interfaceID, zoneID, roleID, siteID, scopeID}
	t.Cleanup(func() {
		for _, objectID := range objectIDs {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM cc_core_object_mutations WHERE object_id=$1`, objectID)
			_, _ = db.ExecContext(context.Background(), `DELETE FROM cc_core_objects WHERE object_id=$1`, objectID)
		}
	})

	requests := []corecontracts.MutationRequest{
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectScope,
			ObjectID: scopeID, ScopeID: "global", OwnerScope: "global",
			Document: json.RawMessage(fmt.Sprintf(`{"id":%q,"kind":"site","name":"Network integration scope","parent_id":"global","delegated_authorities":["configuration","desired-state"]}`, scopeID)),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectSite,
			ObjectID: siteID, ScopeID: scopeID, OwnerScope: "global",
			Document: json.RawMessage(fmt.Sprintf(`{"id":%q,"name":"Network integration site","scope_id":%q}`, siteID, scopeID)),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectRoleAssignment,
			ObjectID: roleID, ScopeID: scopeID, OwnerScope: "global",
			Document: json.RawMessage(fmt.Sprintf(`{"target_node_id":%q,"service_identity_id":%q,"role":"agent","site_id":%q}`, "network-node-"+suffix, "network-agent-"+suffix, siteID)),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectNetworkZone,
			ObjectID: zoneID, ScopeID: scopeID, OwnerScope: "global",
			Document: json.RawMessage(fmt.Sprintf(`{"id":%q,"name":"Integration LAN","kind":"lan","scope_id":%q,"site_id":%q}`, zoneID, scopeID, siteID)),
		},
		{
			Operation: corecontracts.MutationCreate, ObjectType: corecontracts.ObjectNetworkInterface,
			ObjectID: interfaceID, ScopeID: scopeID, OwnerScope: "global",
			Document: json.RawMessage(fmt.Sprintf(`{"id":%q,"node_id":%q,"name":"eth0","kind":"physical","scope_id":%q,"site_id":%q,"network_zone_id":%q,"mac_address":"02:00:00:00:00:01","operational_state":"up","addresses":["192.0.2.10/24"],"mtu":1500,"link_speed_mbps":1000}`, interfaceID, "network-node-"+suffix, scopeID, siteID, zoneID)),
		},
	}
	for index, request := range requests {
		if _, err := repository.Apply(ctx, request, fmt.Sprintf("network-integration-%s-%d", suffix, index)); err != nil {
			t.Fatalf("persist %s %q: %v", request.ObjectType, request.ObjectID, err)
		}
	}

	restarted, err := NewCoreObjectRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Get(ctx, interfaceID)
	if err != nil || loaded.ObjectType != corecontracts.ObjectNetworkInterface || loaded.Generation != 1 {
		t.Fatalf("network interface after adapter restart=%#v err=%v", loaded, err)
	}
	objects, err := restarted.List(ctx, corecontracts.ObjectFilter{})
	if err != nil {
		t.Fatalf("validate restarted distributed snapshot: %v", err)
	}
	foundZone, foundInterface := false, false
	for _, object := range objects {
		foundZone = foundZone || object.ObjectID == zoneID && object.ObjectType == corecontracts.ObjectNetworkZone
		foundInterface = foundInterface || object.ObjectID == interfaceID && object.ObjectType == corecontracts.ObjectNetworkInterface
	}
	if !foundZone || !foundInterface {
		t.Fatalf("restarted snapshot lacks network objects: zone=%v interface=%v", foundZone, foundInterface)
	}
}

func hexDigest(value string) string { return hexDigestBytes([]byte(value)) }
func hexDigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
