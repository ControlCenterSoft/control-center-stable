package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	orchestrationapi "control-center/internal/orchestration/httpapi"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

func TestPostgresVersionedCancellationRejectsStaleJobVersion(t *testing.T) {
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

	var jobsTablePresent bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cc_jobs') IS NOT NULL`).Scan(&jobsTablePresent); err != nil || !jobsTablePresent {
		t.Fatalf("cc_jobs migration is required: present=%v err=%v", jobsTablePresent, err)
	}

	repository, err := NewJobRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewOrchestrationState(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := fmt.Sprintf("%d", now.UnixNano())

	revision, err := state.CreateRevision(
		ctx,
		"cancel-cas-test",
		"revision-cancel-cas-"+suffix,
		"revision-cancel-cas-fingerprint-"+suffix,
		json.RawMessage(`{"generation":1}`),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	changeID := "change-cancel-cas-" + suffix
	persistedChange := orchestrationapi.PersistedChange{
		Snapshot: change.Snapshot{
			ID:         changeID,
			Action:     "service.ensure",
			Requester:  "cancel-cas-test",
			RevisionID: revision.ID,
			Risk:       policy.RiskMedium,
			State:      change.StateQueued,
			Decision: policy.Decision{
				Effect:   policy.EffectAllow,
				Risk:     policy.RiskMedium,
				Reason:   "integration fixture",
				PolicyID: "cancel-cas-policy",
			},
			Version:   1,
			UpdatedAt: now,
		},
		Input:          json.RawMessage(`{}`),
		IdempotencyKey: "change-cancel-cas-key-" + suffix,
		Fingerprint:    "change-cancel-cas-fingerprint-" + suffix,
	}
	if _, created, err := state.CreateChange(ctx, persistedChange); err != nil || !created {
		t.Fatalf("create change created=%v err=%v", created, err)
	}

	created, _, err := repository.Create(ctx, job.CreateRequest{
		ID:             "job-cancel-cas-" + suffix,
		ChangeID:       changeID,
		ActionName:     "service.ensure",
		Input:          json.RawMessage(`{}`),
		IdempotencyKey: "cancel-cas-" + suffix,
		MaxAttempts:    3,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	persistedChange.JobID = created.ID
	persistedChange.Snapshot.Version = 2
	persistedChange.Snapshot.UpdatedAt = now.Add(time.Millisecond)
	if err := state.UpdateChange(ctx, persistedChange); err != nil {
		t.Fatalf("bind cancellation fixture to durable job: %v", err)
	}

	if _, err := repository.RequestCancelIfVersion(ctx, created.ID, created.Version+1, now.Add(time.Second)); !errors.Is(err, job.ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
	unchanged, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Version != created.Version {
		t.Fatalf("stale cancellation mutated job: %#v", unchanged)
	}

	cancelled, err := repository.RequestCancelIfVersion(ctx, created.ID, created.Version, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != job.StatusCancelled || cancelled.Version != created.Version+1 {
		t.Fatalf("unexpected cancellation result: %#v", cancelled)
	}
	persistedChange.Snapshot.State = change.StateCancelled
	persistedChange.Snapshot.Version = 3
	persistedChange.Snapshot.UpdatedAt = now.Add(2 * time.Second)
	if err := state.UpdateChange(ctx, persistedChange); err != nil {
		t.Fatalf("persist terminal cancellation fixture state: %v", err)
	}

	if _, err := repository.RequestCancelIfVersion(ctx, created.ID, created.Version, now.Add(3*time.Second)); !errors.Is(err, job.ErrVersionConflict) {
		t.Fatalf("replayed stale cancellation error = %v, want ErrVersionConflict", err)
	}
}
