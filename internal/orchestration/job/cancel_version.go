package job

import (
	"context"
	"errors"
	"time"
)

// RequestCancelIfVersion applies the existing cancellation transition only when
// the Job is still at the exact version reviewed by the caller. This prevents a
// stale operator view from cancelling a Job that has since been claimed,
// retried, completed, or otherwise advanced.
func (r *MemoryRepository) RequestCancelIfVersion(ctx context.Context, id string, expectedVersion uint64, now time.Time) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if expectedVersion == 0 {
		return Job{}, errors.New("expected job version is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	found, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if found.Version != expectedVersion {
		return Job{}, ErrVersionConflict
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

var _ VersionedCancellationRepository = (*MemoryRepository)(nil)
