package operationsview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"control-center/internal/orchestration/job"
)

var (
	ErrJobRetryAdmissionStale    = errors.New("operations job retry admission is stale")
	ErrJobRetryAdmissionTampered = errors.New("operations job retry admission integrity failure")
)

// JobRetryCurrentEvidenceSource supplies the authoritative evidence that must
// be re-read immediately before a new manual retry mutation. Implementations
// must return current durable/canonical evidence and must not mutate the Job,
// Change or approval state as a side effect of these reads.
type JobRetryCurrentEvidenceSource interface {
	CurrentRevision(context.Context, job.Job) (revisionID string, revisionDigest string, err error)
	CurrentRetryPolicy(context.Context, job.Job) (JobRetryPolicyEvidence, error)
	CurrentRetryHistory(context.Context, job.Job) (job.ManualRetryHistoryEvidence, error)
}

// JobRetryMutationRequest binds one mutation attempt to the exact admission
// evidence the operator reviewed. RetryJobID and RetryIdempotencyKey identify a
// fresh child Job; neither value grants execution authority.
type JobRetryMutationRequest struct {
	Reviewed            JobRetryAdmissionEvidence
	RetryJobID          string
	RetryIdempotencyKey string
	RequestedAt         time.Time
}

// CreateManualRetryWithRevalidation turns an eligible review decision into one
// fresh queued Job lineage only after re-reading all mutable evidence. It does
// not claim or execute the child Job and does not expose raw Job input/output,
// failure detail, lease material, credentials, secrets or tokens.
func CreateManualRetryWithRevalidation(
	ctx context.Context,
	repository job.ManualRetryRepository,
	evidenceSource JobRetryCurrentEvidenceSource,
	request JobRetryMutationRequest,
) (job.Job, job.ManualRetryLineage, bool, error) {
	if repository == nil {
		return job.Job{}, job.ManualRetryLineage{}, false, errors.New("manual retry repository is required")
	}
	if request.RequestedAt.IsZero() {
		return job.Job{}, job.ManualRetryLineage{}, false, errors.New("manual retry request time is required")
	}
	if err := validateReviewedRetryAdmission(request.Reviewed); err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}

	// A committed lineage is authoritative evidence that this exact reviewed
	// admission already crossed the atomic mutation boundary. Return it before
	// re-reading policy/history so a lost HTTP response can be retried even
	// after retry-history counters have advanced or an evidence provider is
	// temporarily unavailable. Different child identity still fails closed.
	existing, err := repository.GetManualRetryLineageByAdmission(ctx, request.Reviewed.AdmissionID)
	if err == nil {
		retry, getErr := repository.Get(ctx, existing.RetryJobID)
		if getErr != nil {
			return job.Job{}, job.ManualRetryLineage{}, false, getErr
		}
		if request.RetryJobID != existing.RetryJobID || request.RetryIdempotencyKey != retry.IdempotencyKey {
			return job.Job{}, job.ManualRetryLineage{}, false, job.ErrManualRetryAdmissionConflict
		}
		return retry, existing, false, nil
	}
	if !errors.Is(err, job.ErrManualRetryLineageNotFound) {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	if evidenceSource == nil {
		return job.Job{}, job.ManualRetryLineage{}, false, errors.New("retry evidence source is required")
	}

	source, err := repository.Get(ctx, request.Reviewed.JobID)
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	if source.Version != request.Reviewed.JobVersion {
		return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: source Job version changed", ErrJobRetryAdmissionStale)
	}
	if source.ID != request.Reviewed.JobID || source.ChangeID != request.Reviewed.ChangeID || source.ActionName != request.Reviewed.ActionName || source.Status != request.Reviewed.SourceStatus || source.Attempt != request.Reviewed.SourceAttempt || source.MaxAttempts != request.Reviewed.SourceMaxAttempts {
		return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: source Job identity or retry state changed", ErrJobRetryAdmissionStale)
	}

	revisionID, revisionDigest, err := evidenceSource.CurrentRevision(ctx, source)
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	policy, err := evidenceSource.CurrentRetryPolicy(ctx, source)
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	history, err := evidenceSource.CurrentRetryHistory(ctx, source)
	if err != nil {
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	if history.ContractVersion != job.ManualRetryHistoryContractVersion || history.SourceJobID != source.ID || history.SourceJobVersion != source.Version {
		return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: retry history is not bound to the exact source Job", ErrJobRetryAdmissionStale)
	}
	if policy.UsedManualRetries != history.UsedManualRetries {
		return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: policy retry usage does not match durable retry history", ErrJobRetryAdmissionStale)
	}

	current, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    source,
		ExpectedJobVersion:     request.Reviewed.JobVersion,
		RevisionID:             revisionID,
		RevisionDigest:         revisionDigest,
		Policy:                 policy,
		RetryHistoryDigest:     history.Digest,
		RetryHistoryObservedAt: history.ObservedAt,
		SourceAlreadyRetried:   history.SourceAlreadyRetried,
		ObservedAt:             request.RequestedAt,
	})
	if err != nil {
		if errors.Is(err, job.ErrVersionConflict) || errors.Is(err, ErrInvalidJobRetryAdmission) {
			return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: %v", ErrJobRetryAdmissionStale, err)
		}
		return job.Job{}, job.ManualRetryLineage{}, false, err
	}
	if current.State != JobRetryAdmissionEligible || len(current.Blockers) != 0 {
		return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: current retry evidence is blocked", ErrJobRetryAdmissionStale)
	}
	if !sameReviewedRetryBoundary(request.Reviewed, current) {
		return job.Job{}, job.ManualRetryLineage{}, false, fmt.Errorf("%w: reviewed revision, policy, approval or retry history changed", ErrJobRetryAdmissionStale)
	}

	mutation := job.ManualRetryRequest{
		SourceJobID:             source.ID,
		ExpectedSourceVersion:   source.Version,
		RetryJobID:              request.RetryJobID,
		RetryIdempotencyKey:     request.RetryIdempotencyKey,
		ReviewedAdmissionID:     request.Reviewed.AdmissionID,
		RevalidationAdmissionID: current.AdmissionID,
		RevisionID:              current.RevisionID,
		RevisionDigest:          current.RevisionDigest,
		PolicyID:                current.PolicyID,
		PolicyDigest:            current.PolicyDigest,
		RetryHistoryDigest:      current.RetryHistoryDigest,
		ApprovalEvidenceDigest:  current.ApprovalEvidenceDigest,
		RequestedAt:             request.RequestedAt,
	}
	return repository.CreateManualRetry(ctx, mutation)
}

func validateReviewedRetryAdmission(reviewed JobRetryAdmissionEvidence) error {
	if reviewed.ContractVersion != JobRetryAdmissionContractVersion {
		return fmt.Errorf("%w: unsupported retry admission contract", ErrJobRetryAdmissionTampered)
	}
	if reviewed.State != JobRetryAdmissionEligible || len(reviewed.Blockers) != 0 {
		return fmt.Errorf("%w: reviewed admission is not eligible", ErrJobRetryAdmissionStale)
	}
	if reviewed.AdmissionID == "" {
		return fmt.Errorf("%w: missing admission identity", ErrJobRetryAdmissionTampered)
	}
	expected, err := retryAdmissionDigest(reviewed)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrJobRetryAdmissionTampered, err)
	}
	if expected != reviewed.AdmissionID {
		return fmt.Errorf("%w: admission digest mismatch", ErrJobRetryAdmissionTampered)
	}
	return nil
}

func sameReviewedRetryBoundary(reviewed, current JobRetryAdmissionEvidence) bool {
	return reviewed.JobID == current.JobID &&
		reviewed.JobVersion == current.JobVersion &&
		reviewed.ChangeID == current.ChangeID &&
		reviewed.ActionName == current.ActionName &&
		reviewed.RevisionID == current.RevisionID &&
		reviewed.RevisionDigest == current.RevisionDigest &&
		reviewed.SourceStatus == current.SourceStatus &&
		reviewed.SourceAttempt == current.SourceAttempt &&
		reviewed.SourceMaxAttempts == current.SourceMaxAttempts &&
		reviewed.SourceAlreadyRetried == current.SourceAlreadyRetried &&
		reviewed.PolicyID == current.PolicyID &&
		reviewed.PolicyDigest == current.PolicyDigest &&
		reviewed.RetryHistoryDigest == current.RetryHistoryDigest &&
		reviewed.UsedManualRetries == current.UsedManualRetries &&
		reviewed.MaxManualRetries == current.MaxManualRetries &&
		reviewed.RequiresFreshApproval == current.RequiresFreshApproval &&
		reviewed.ApprovalEvidenceDigest == current.ApprovalEvidenceDigest
}
