package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"control-center/internal/orchestration/events"
)

type idempotencyRecord struct {
	jobID       string
	fingerprint string
}
type MemoryRepository struct {
	mu          sync.RWMutex
	jobs        map[string]Job
	idempotency map[string]idempotencyRecord
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{jobs: make(map[string]Job), idempotency: make(map[string]idempotencyRecord)}
}
func (r *MemoryRepository) Create(ctx context.Context, request CreateRequest) (Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, false, err
	}
	if request.ID == "" || request.ChangeID == "" || request.ActionName == "" || request.IdempotencyKey == "" {
		return Job{}, false, errors.New("job id, change id, action, and idempotency key are required")
	}
	if len(request.Input) == 0 || !json.Valid(request.Input) {
		return Job{}, false, errors.New("job input must be valid JSON")
	}
	if request.MaxAttempts < 1 || request.Now.IsZero() {
		return Job{}, false, errors.New("max attempts and creation time are required")
	}
	fingerprint := fingerprint(request)
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.idempotency[request.IdempotencyKey]; ok {
		if record.fingerprint != fingerprint {
			return Job{}, false, ErrIdempotencyConflict
		}
		return clone(r.jobs[record.jobID]), false, nil
	}
	if _, exists := r.jobs[request.ID]; exists {
		return Job{}, false, fmt.Errorf("job id already exists: %s", request.ID)
	}
	now := request.Now.UTC()
	created := Job{ID: request.ID, ChangeID: request.ChangeID, ActionName: request.ActionName, Input: append(json.RawMessage(nil), request.Input...), IdempotencyKey: request.IdempotencyKey, Status: StatusQueued, MaxAttempts: request.MaxAttempts, CreatedAt: now, UpdatedAt: now, Version: 1}
	r.jobs[created.ID] = created
	r.idempotency[request.IdempotencyKey] = idempotencyRecord{jobID: created.ID, fingerprint: fingerprint}
	return clone(created), true, nil
}
func (r *MemoryRepository) Get(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	found, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return clone(found), nil
}
func (r *MemoryRepository) List(ctx context.Context, filter Filter) ([]Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Job, 0, len(r.jobs))
	for _, candidate := range r.jobs {
		if filter.ChangeID != "" && candidate.ChangeID != filter.ChangeID {
			continue
		}
		if filter.Status != "" && candidate.Status != filter.Status {
			continue
		}
		result = append(result, clone(candidate))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}
func (r *MemoryRepository) Claim(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, false, err
	}
	if workerID == "" || now.IsZero() || ttl <= 0 {
		return Job{}, false, errors.New("worker id, current time, and positive lease ttl are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.jobs))
	for id := range r.jobs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := r.jobs[ids[i]], r.jobs[ids[j]]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	for _, id := range ids {
		candidate := r.jobs[id]
		if candidate.Status == StatusCancelRequested && candidate.Lease != nil && !candidate.Lease.ExpiresAt.After(now) {
			candidate.Status = StatusCancelled
			candidate.Lease = nil
			candidate.UpdatedAt = now.UTC()
			candidate.Version++
			r.jobs[id] = candidate
			continue
		}
		eligible := candidate.Status == StatusQueued || (candidate.Status == StatusRetryWait && !candidate.NextAttemptAt.After(now)) || (candidate.Status == StatusRunning && candidate.Lease != nil && !candidate.Lease.ExpiresAt.After(now))
		if !eligible {
			continue
		}
		if candidate.Attempt >= candidate.MaxAttempts {
			candidate.Status = StatusFailed
			candidate.LastError = "maximum attempts exhausted"
			candidate.Lease = nil
			candidate.UpdatedAt = now.UTC()
			candidate.Version++
			r.jobs[id] = candidate
			continue
		}
		candidate.Attempt++
		candidate.Status = StatusRunning
		candidate.NextAttemptAt = time.Time{}
		candidate.LastError = ""
		candidate.Version++
		candidate.UpdatedAt = now.UTC()
		candidate.Lease = &Lease{Token: leaseToken(candidate, workerID, now), WorkerID: workerID, ExpiresAt: now.UTC().Add(ttl)}
		r.jobs[id] = candidate
		return clone(candidate), true, nil
	}
	return Job{}, false, nil
}
func (r *MemoryRepository) RenewLease(ctx context.Context, id, token string, now time.Time, ttl time.Duration) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if ttl <= 0 {
		return Job{}, errors.New("lease ttl must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	found, err := r.leased(id, token, now)
	if err != nil {
		return Job{}, err
	}
	found.Lease.ExpiresAt = now.UTC().Add(ttl)
	found.UpdatedAt = now.UTC()
	found.Version++
	r.jobs[id] = found
	return clone(found), nil
}
func (r *MemoryRepository) Succeed(ctx context.Context, id, token string, output events.Output, now time.Time) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	found, err := r.leased(id, token, now)
	if err != nil {
		return Job{}, err
	}
	if found.Status == StatusCancelRequested {
		found.Status = StatusCancelled
	} else {
		found.Status = StatusSucceeded
		copy := cloneOutput(output)
		found.Output = &copy
	}
	found.Lease = nil
	found.UpdatedAt = now.UTC()
	found.Version++
	r.jobs[id] = found
	return clone(found), nil
}
func (r *MemoryRepository) Fail(ctx context.Context, id, token, message string, output events.Output, policy RetryPolicy, now time.Time) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if message == "" {
		return Job{}, errors.New("failure message is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	found, err := r.leased(id, token, now)
	if err != nil {
		return Job{}, err
	}
	found.LastError = message
	copy := cloneOutput(output)
	found.Output = &copy
	found.Lease = nil
	if found.Status == StatusCancelRequested {
		found.Status = StatusCancelled
	} else if found.Attempt < found.MaxAttempts {
		found.Status = StatusRetryWait
		found.NextAttemptAt = now.UTC().Add(retryDelay(found.Attempt, policy))
	} else {
		found.Status = StatusFailed
		found.NextAttemptAt = time.Time{}
	}
	found.UpdatedAt = now.UTC()
	found.Version++
	r.jobs[id] = found
	return clone(found), nil
}
func (r *MemoryRepository) RequestCancel(ctx context.Context, id string, now time.Time) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	found, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if found.Status.Terminal() {
		return clone(found), nil
	}
	if found.Status == StatusRunning {
		found.Status = StatusCancelRequested
	} else {
		found.Status = StatusCancelled
		found.Lease = nil
	}
	found.UpdatedAt = now.UTC()
	found.Version++
	r.jobs[id] = found
	return clone(found), nil
}
func (r *MemoryRepository) leased(id, token string, now time.Time) (Job, error) {
	found, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if found.Status != StatusRunning && found.Status != StatusCancelRequested {
		return Job{}, ErrLeaseLost
	}
	if found.Lease == nil || found.Lease.Token != token || !found.Lease.ExpiresAt.After(now) {
		return Job{}, ErrLeaseLost
	}
	return found, nil
}
func fingerprint(request CreateRequest) string {
	h := sha256.New()
	h.Write([]byte(request.ChangeID))
	h.Write([]byte{0})
	h.Write([]byte(request.ActionName))
	h.Write([]byte{0})
	h.Write(request.Input)
	return hex.EncodeToString(h.Sum(nil))
}
func leaseToken(candidate Job, workerID string, now time.Time) string {
	value := fmt.Sprintf("%s:%s:%d:%d", candidate.ID, workerID, candidate.Version, now.UnixNano())
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func retryDelay(attempt int, policy RetryPolicy) time.Duration {
	base := policy.BaseDelay
	if base <= 0 {
		base = time.Second
	}
	delay := base
	for i := 1; i < attempt; i++ {
		if delay > (1 << 62) {
			break
		}
		delay *= 2
	}
	if policy.MaxDelay > 0 && delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}
func clone(source Job) Job {
	copy := source
	copy.Input = append(json.RawMessage(nil), source.Input...)
	if source.Lease != nil {
		lease := *source.Lease
		copy.Lease = &lease
	}
	if source.Output != nil {
		output := cloneOutput(*source.Output)
		copy.Output = &output
	}
	return copy
}
func cloneOutput(source events.Output) events.Output {
	copy := events.Output{ActualStates: append([]events.ActualState(nil), source.ActualStates...), Health: append([]events.Health(nil), source.Health...), AuditEvents: append([]events.AuditEvent(nil), source.AuditEvents...)}
	for i := range copy.ActualStates {
		copy.ActualStates[i].Details = append(json.RawMessage(nil), source.ActualStates[i].Details...)
	}
	for i := range copy.AuditEvents {
		copy.AuditEvents[i].Details = append(json.RawMessage(nil), source.AuditEvents[i].Details...)
	}
	return copy
}
