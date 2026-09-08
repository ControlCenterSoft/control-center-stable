package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

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
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cc_jobs') IS NOT NULL AND to_regclass('cc_local_users') IS NOT NULL`).Scan(&migrationPresent); err != nil || !migrationPresent {
		t.Fatalf("database must be migrated through 0004 before integration tests: present=%v err=%v", migrationPresent, err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash("integration-test-password-12345")
	if err != nil {
		t.Fatal(err)
	}
	username := "integration-" + suffix
	userID, err := BootstrapAdmin(ctx, db, username, passwordHash, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := BootstrapAdmin(ctx, db, username, passwordHash, time.Now().UTC())
	if err != nil || secondID != userID {
		t.Fatalf("idempotent bootstrap id=%q second=%q err=%v", userID, secondID, err)
	}
	identities, _ := NewIdentityStore(db)
	if _, err := identities.FindUserByUsername(ctx, username); err != nil {
		t.Fatal(err)
	}
	if !NewAuthorizer(db).Allowed(userID, rbac.PermissionChangesWrite, rbac.GlobalScope()) {
		t.Fatal("persisted administrator binding did not authorize")
	}
	session := auth.Session{
		ID: deterministicUUID("session:" + suffix), UserID: userID, TokenDigest: hexDigest("token:" + suffix),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), SourceIP: "127.0.0.1", UserAgent: "integration-test",
	}
	if err := identities.CreateSession(ctx, session); err != nil {
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
	restartedAuditLog, _ := NewAuditLog(db)
	if err := restartedAuditLog.VerifyChain(ctx); err != nil {
		t.Fatalf("audit chain after restart: %v", err)
	}

	state, _ := NewOrchestrationState(db)
	content := json.RawMessage(`{"zone": "b", "generation": 1}`)
	fingerprint := hexDigestBytes(content)
	revision, err := state.CreateRevision(ctx, userID, "revision-"+suffix, fingerprint, content, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Decision{
		Effect: policy.EffectAllow, Risk: policy.RiskHigh, Reason: "integration test", PolicyID: "integration-v1",
		Requirement: policy.ApprovalRequirement{Minimum: 1, Permission: string(rbac.PermissionChangesApprove), DistinctActors: true},
	}
	changeID := "chg-integration-" + suffix
	persisted := orchestrationapi.PersistedChange{
		Snapshot: change.Snapshot{
			ID: changeID, Action: "resource.record", Requester: userID, RevisionID: revision.ID,
			Risk: policy.RiskHigh, State: change.StatePendingApproval, Decision: decision, Version: 1, UpdatedAt: time.Now().UTC(),
		},
		Input:          json.RawMessage(`{"resourceId":"node-1","kind":"node","state":"present"}`),
		IdempotencyKey: "change-" + suffix, Fingerprint: hexDigest("change-input:" + suffix),
	}
	if _, created, err := state.CreateChange(ctx, persisted); err != nil || !created {
		t.Fatalf("create change created=%v err=%v", created, err)
	}
	persisted.Snapshot.State = change.StateApproved
	persisted.Snapshot.Version = 2
	persisted.Snapshot.Approvals = []policy.Approval{{
		Actor: userID, Permissions: []string{string(rbac.PermissionChangesApprove)}, ApprovedAt: time.Now().UTC(),
	}}
	jobs, _ := NewJobRepository(db)
	createdJob, _, err := jobs.Create(ctx, job.CreateRequest{
		ID: "job-integration-" + suffix, ChangeID: changeID, ActionName: "resource.record", Input: persisted.Input,
		IdempotencyKey: "job-" + suffix, MaxAttempts: 2, Now: time.Now().UTC(),
	})
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
	observedAt := time.Now().UTC()
	output := events.Output{
		ActualStates: []events.ActualState{{
			ResourceID: "node-1", Kind: "node", State: events.StatePresent, ObservedAt: observedAt,
		}},
		Health: []events.Health{{
			ResourceID: "node-1", Status: events.HealthHealthy, CheckedAt: observedAt,
		}},
	}
	if _, err := jobs.Succeed(ctx, claimed.ID, claimed.Lease.Token, output, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	restartedState, _ := NewOrchestrationState(db)
	loaded, err := restartedState.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Revisions) != 1 || string(loaded.Revisions[0].Content) != string(content) {
		t.Fatalf("configuration revision bytes changed across database round trip: %#v", loaded.Revisions)
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
	_, err = orchestrationapi.New(orchestrationapi.Config{
		Registry: action.NewRegistry(), Jobs: restartedJobs, Persistence: restartedState,
		Evaluator:  policy.ThresholdEvaluator{PolicyID: "integration-v1", ApprovalPermission: string(rbac.PermissionChangesApprove)},
		Protect:    func(_ rbac.Permission, next http.Handler) http.Handler { return next },
		Actor:      func(*http.Request) (string, bool) { return userID, true },
		Middleware: func(next http.Handler) http.Handler { return next },
		Context:    ctx,
	})
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

func hexDigest(value string) string { return hexDigestBytes([]byte(value)) }

func hexDigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
