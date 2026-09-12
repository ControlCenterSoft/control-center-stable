package job

import "context"

func (r *MemoryManualRetryRepository) GetManualRetryLineageByRetryJob(ctx context.Context, retryJobID string) (ManualRetryLineage, error) {
	if err := ctx.Err(); err != nil {
		return ManualRetryLineage{}, err
	}
	r.jobs.mu.RLock()
	defer r.jobs.mu.RUnlock()
	found, ok := r.byRetryJob[retryJobID]
	if !ok {
		return ManualRetryLineage{}, ErrManualRetryLineageNotFound
	}
	return found, nil
}

var _ ManualRetryLineageReader = (*MemoryManualRetryRepository)(nil)
