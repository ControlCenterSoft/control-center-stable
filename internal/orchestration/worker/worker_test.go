package worker_test

import (
	"context"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
	"control-center/internal/orchestration/worker"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

type countingRepository struct {
	*job.MemoryRepository
	renewals atomic.Int32
}

func (r *countingRepository) RenewLease(ctx context.Context, id, token string, now time.Time, ttl time.Duration) (job.Job, error) {
	renewed, err := r.MemoryRepository.RenewLease(ctx, id, token, now, ttl)
	if err == nil {
		r.renewals.Add(1)
	}
	return renewed, err
}
func TestLongActionRenewsLeaseAndCarriesStableDownstreamIdempotency(t *testing.T) {
	repository := &countingRepository{MemoryRepository: job.NewMemoryRepository()}
	now := time.Now().UTC()
	_, _, err := repository.Create(context.Background(), job.CreateRequest{ID: "job-long", ChangeID: "change-long", ActionName: "test.long", Input: json.RawMessage(`{}`), IdempotencyKey: "client-operation-42", MaxAttempts: 2, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	registry := action.NewRegistry()
	definition := action.NewTyped("test.long", "test.execute", policy.RiskMedium, json.RawMessage(`{"type":"object"}`), func(ctx context.Context, _ struct{}) (events.Output, error) {
		invocation, ok := action.InvocationFromContext(ctx)
		if !ok {
			t.Fatal("missing invocation context")
		}
		first, err := invocation.DownstreamIdempotencyKey("provider/apply")
		if err != nil {
			t.Fatal(err)
		}
		retry := invocation
		retry.Attempt++
		second, err := retry.DownstreamIdempotencyKey("provider/apply")
		if err != nil || first != second {
			t.Fatalf("downstream key changed across retry: %q != %q (err=%v)", first, second, err)
		}
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for repository.renewals.Load() < 2 {
			select {
			case <-ctx.Done():
				return events.Output{}, ctx.Err()
			case <-deadline.C:
				t.Fatal("worker did not renew the lease during a long action")
			case <-ticker.C:
			}
		}
		return events.Output{}, nil
	}, func(context.Context, struct{}, events.Output) error { return nil })
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	runner := worker.Worker{ID: "worker-long", Repository: repository, Registry: registry, Allowlist: worker.Set("test.long"), Permissions: worker.Set("test.execute"), LeaseTTL: 30 * time.Millisecond, RetryPolicy: job.RetryPolicy{BaseDelay: time.Millisecond}, Failures: worker.NoFailures{}}
	completed, claimed, err := runner.RunOne(context.Background(), now)
	if err != nil || !claimed || completed.Status != job.StatusSucceeded {
		t.Fatalf("run result=%#v claimed=%v err=%v", completed, claimed, err)
	}
	if repository.renewals.Load() < 2 {
		t.Fatalf("renewals=%d, want at least 2", repository.renewals.Load())
	}
}
