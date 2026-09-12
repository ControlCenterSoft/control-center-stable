package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestVersionedCancellationRejectsStaleOperatorState(t *testing.T) {
	repository := job.NewMemoryRepository()
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()

	created, _, err := repository.Create(ctx, job.CreateRequest{
		ID:             "job-cas",
		ChangeID:       "change-cas",
		ActionName:     "service.ensure",
		Input:          json.RawMessage(`{}`),
		IdempotencyKey: "cancel-cas",
		MaxAttempts:    3,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != 1 || created.Status != job.StatusQueued {
		t.Fatalf("unexpected created job: %#v", created)
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

	if _, err := repository.RequestCancelIfVersion(ctx, created.ID, created.Version, now.Add(3*time.Second)); !errors.Is(err, job.ErrVersionConflict) {
		t.Fatalf("replayed stale cancellation error = %v, want ErrVersionConflict", err)
	}
	terminal, err := repository.RequestCancelIfVersion(ctx, created.ID, cancelled.Version, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != job.StatusCancelled || terminal.Version != cancelled.Version || !terminal.UpdatedAt.Equal(cancelled.UpdatedAt) {
		t.Fatalf("current terminal cancellation should be idempotent: %#v", terminal)
	}
}

func TestVersionedCancellationBindsRunningJobVersion(t *testing.T) {
	repository := job.NewMemoryRepository()
	ctx := context.Background()
	now := time.Unix(200, 0).UTC()

	created, _, err := repository.Create(ctx, job.CreateRequest{
		ID:             "job-running",
		ChangeID:       "change-running",
		ActionName:     "service.ensure",
		Input:          json.RawMessage(`{}`),
		IdempotencyKey: "cancel-running",
		MaxAttempts:    3,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := repository.Claim(ctx, "worker-a", now.Add(time.Second), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %#v ok=%v err=%v", claimed, ok, err)
	}

	if _, err := repository.RequestCancelIfVersion(ctx, created.ID, created.Version, now.Add(2*time.Second)); !errors.Is(err, job.ErrVersionConflict) {
		t.Fatalf("pre-claim version must not cancel running job: %v", err)
	}
	cancelRequested, err := repository.RequestCancelIfVersion(ctx, claimed.ID, claimed.Version, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if cancelRequested.Status != job.StatusCancelRequested || cancelRequested.Version != claimed.Version+1 || cancelRequested.Lease == nil {
		t.Fatalf("unexpected running cancellation result: %#v", cancelRequested)
	}
}
