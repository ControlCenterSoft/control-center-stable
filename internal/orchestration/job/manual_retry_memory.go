package job

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type memoryManualRetryRecord struct {
	fingerprint string
	lineage     ManualRetryLineage
}

// MemoryManualRetryRepository layers the manual-retry lineage contract over the
// existing in-memory Job repository. Both Job creation and lineage persistence
// are guarded by the same Job mutex, so tests exercise the same all-or-nothing
// boundary expected from the PostgreSQL implementation.
type MemoryManualRetryRepository struct {
	jobs        *MemoryRepository
	byAdmission map[string]memoryManualRetryRecord
	byRetryJob  map[string]ManualRetryLineage
	bySource    map[string]ManualRetryLineage
}

func NewMemoryManualRetryRepository(jobs *MemoryRepository) (*MemoryManualRetryRepository, error) {
	if jobs == nil {
		return nil, errors.New("job repository is required")
	}
	return &MemoryManualRetryRepository{
		jobs:        jobs,
		byAdmission: make(map[string]memoryManualRetryRecord),
		byRetryJob:  make(map[string]ManualRetryLineage),
		bySource:    make(map[string]ManualRetryLineage),
	}, nil
}

func (r *MemoryManualRetryRepository) Get(ctx context.Context, id string) (Job, error) {
	return r.jobs.Get(ctx, id)
}

func (r *MemoryManualRetryRepository) CreateManualRetry(ctx context.Context, request ManualRetryRequest) (Job, ManualRetryLineage, bool, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, ManualRetryLineage{}, false, err
	}
	fingerprintValue, err := ManualRetryRequestFingerprint(request)
	if err != nil {
		return Job{}, ManualRetryLineage{}, false, err
	}

	r.jobs.mu.Lock()
	defer r.jobs.mu.Unlock()

	if existing, ok := r.byAdmission[request.ReviewedAdmissionID]; ok {
		if existing.fingerprint != fingerprintValue {
			return Job{}, ManualRetryLineage{}, false, ErrManualRetryAdmissionConflict
		}
		retry, ok := r.jobs.jobs[existing.lineage.RetryJobID]
		if !ok {
			return Job{}, ManualRetryLineage{}, false, fmt.Errorf("manual retry lineage references missing retry job %q", existing.lineage.RetryJobID)
		}
		return clone(retry), existing.lineage, false, nil
	}

	source, ok := r.jobs.jobs[request.SourceJobID]
	if !ok {
		return Job{}, ManualRetryLineage{}, false, ErrNotFound
	}
	if source.Version != request.ExpectedSourceVersion {
		return Job{}, ManualRetryLineage{}, false, ErrVersionConflict
	}
	if source.Status != StatusFailed || source.Attempt < source.MaxAttempts || source.Lease != nil {
		return Job{}, ManualRetryLineage{}, false, ErrManualRetrySourceNotFailed
	}
	if _, exists := r.bySource[manualRetrySourceKey(source.ID, source.Version)]; exists {
		return Job{}, ManualRetryLineage{}, false, ErrManualRetrySourceAlreadyRetried
	}
	if _, exists := r.jobs.jobs[request.RetryJobID]; exists {
		return Job{}, ManualRetryLineage{}, false, fmt.Errorf("retry job id already exists: %s", request.RetryJobID)
	}
	if _, exists := r.jobs.idempotency[request.RetryIdempotencyKey]; exists {
		return Job{}, ManualRetryLineage{}, false, ErrIdempotencyConflict
	}

	rootJobID := source.ID
	if parent, ok := r.byRetryJob[source.ID]; ok {
		rootJobID = parent.RootJobID
	}
	now := request.RequestedAt.UTC()
	retryRequest := CreateRequest{
		ID:             request.RetryJobID,
		ChangeID:       source.ChangeID,
		ActionName:     source.ActionName,
		Input:          append([]byte(nil), source.Input...),
		IdempotencyKey: request.RetryIdempotencyKey,
		MaxAttempts:    source.MaxAttempts,
		Now:            now,
	}
	retry := Job{
		ID:             retryRequest.ID,
		ChangeID:       retryRequest.ChangeID,
		ActionName:     retryRequest.ActionName,
		Input:          append([]byte(nil), retryRequest.Input...),
		IdempotencyKey: retryRequest.IdempotencyKey,
		Status:         StatusQueued,
		MaxAttempts:    retryRequest.MaxAttempts,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	lineage := ManualRetryLineage{
		RootJobID:               rootJobID,
		SourceJobID:             source.ID,
		SourceJobVersion:        source.Version,
		RetryJobID:              retry.ID,
		RetryIdempotencyKey:     retry.IdempotencyKey,
		ReviewedAdmissionID:     request.ReviewedAdmissionID,
		RevalidationAdmissionID: request.RevalidationAdmissionID,
		RevisionID:              request.RevisionID,
		RevisionDigest:          request.RevisionDigest,
		PolicyID:                request.PolicyID,
		PolicyDigest:            request.PolicyDigest,
		RetryHistoryDigest:      request.RetryHistoryDigest,
		ApprovalEvidenceDigest:  request.ApprovalEvidenceDigest,
		RequestedAt:             now,
	}

	r.jobs.jobs[retry.ID] = retry
	r.jobs.idempotency[retry.IdempotencyKey] = idempotencyRecord{
		jobID:       retry.ID,
		fingerprint: fingerprint(retryRequest),
	}
	r.byAdmission[request.ReviewedAdmissionID] = memoryManualRetryRecord{
		fingerprint: fingerprintValue,
		lineage:     lineage,
	}
	r.byRetryJob[retry.ID] = lineage
	r.bySource[manualRetrySourceKey(source.ID, source.Version)] = lineage
	return clone(retry), lineage, true, nil
}

func (r *MemoryManualRetryRepository) GetManualRetryLineageByAdmission(ctx context.Context, admissionID string) (ManualRetryLineage, error) {
	if err := ctx.Err(); err != nil {
		return ManualRetryLineage{}, err
	}
	r.jobs.mu.RLock()
	defer r.jobs.mu.RUnlock()
	found, ok := r.byAdmission[admissionID]
	if !ok {
		return ManualRetryLineage{}, ErrManualRetryLineageNotFound
	}
	return found.lineage, nil
}

func (r *MemoryManualRetryRepository) ListManualRetryLineage(ctx context.Context, rootJobID string) ([]ManualRetryLineage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateManualRetryIdentifier("root job id", rootJobID); err != nil {
		return nil, err
	}
	r.jobs.mu.RLock()
	defer r.jobs.mu.RUnlock()
	result := make([]ManualRetryLineage, 0)
	for _, record := range r.byAdmission {
		if record.lineage.RootJobID == rootJobID {
			result = append(result, record.lineage)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RequestedAt.Equal(result[j].RequestedAt) {
			return result[i].RetryJobID < result[j].RetryJobID
		}
		return result[i].RequestedAt.Before(result[j].RequestedAt)
	})
	return result, nil
}

func manualRetrySourceKey(jobID string, version uint64) string {
	return fmt.Sprintf("%s\x00%d", jobID, version)
}

var _ ManualRetryRepository = (*MemoryManualRetryRepository)(nil)
