package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

func TestConcurrentIdempotentCreateAndSingleClaim(t *testing.T) {
	repository := job.NewMemoryRepository()
	ctx := context.Background()
	now := time.Unix(100, 0)
	var created atomic.Int32
	var ids sync.Map
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, wasCreated, err := repository.Create(ctx, job.CreateRequest{ID: fmt.Sprintf("job-%d", i), ChangeID: "change-1", ActionName: "service.ensure", Input: json.RawMessage(`{"name":"api"}`), IdempotencyKey: "client-request-1", MaxAttempts: 3, Now: now})
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			if wasCreated {
				created.Add(1)
			}
			ids.Store(got.ID, struct{}{})
		}(i)
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created count = %d, want 1", created.Load())
	}
	count := 0
	ids.Range(func(_, _ any) bool { count++; return true })
	if count != 1 {
		t.Fatalf("returned %d distinct jobs, want 1", count)
	}
	var claims atomic.Int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ok, err := repository.Claim(ctx, fmt.Sprintf("worker-%d", i), now, time.Minute)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			if ok {
				claims.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("claim count = %d, want 1", claims.Load())
	}
}
func TestRetryLeaseAndCancellation(t *testing.T) {
	repository := job.NewMemoryRepository()
	ctx := context.Background()
	now := time.Unix(100, 0)
	created, _, err := repository.Create(ctx, job.CreateRequest{ID: "job-1", ChangeID: "change-1", ActionName: "test", Input: json.RawMessage(`{}`), IdempotencyKey: "key", MaxAttempts: 2, Now: now})
	if err != nil || created.Status != job.StatusQueued {
		t.Fatalf("create = %#v, %v", created, err)
	}
	claimed, ok, err := repository.Claim(ctx, "worker", now, time.Minute)
	if err != nil || !ok || claimed.Attempt != 1 {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	if _, err := repository.RenewLease(ctx, claimed.ID, "wrong", now, time.Minute); !errors.Is(err, job.ErrLeaseLost) {
		t.Fatalf("wrong lease error = %v", err)
	}
	retried, err := repository.Fail(ctx, claimed.ID, claimed.Lease.Token, "temporary", events.Output{}, job.RetryPolicy{BaseDelay: time.Second}, now)
	if err != nil || retried.Status != job.StatusRetryWait || !retried.NextAttemptAt.Equal(now.Add(time.Second)) {
		t.Fatalf("retry = %#v, %v", retried, err)
	}
	cancelled, err := repository.RequestCancel(ctx, claimed.ID, now)
	if err != nil || cancelled.Status != job.StatusCancelled {
		t.Fatalf("cancel = %#v, %v", cancelled, err)
	}
}
