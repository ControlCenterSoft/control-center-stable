package operationsview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/orchestration/job"
)

const (
	JobRetryAdmissionContractVersion     = "ui.operations-job-retry-admission/v1"
	MaxJobRetryAdmissionIdentifierLength = 255
	MaxJobRetryAdmissionBlockers         = 8
)

var ErrInvalidJobRetryAdmission = errors.New("invalid operations job retry admission")

type JobRetryAdmissionState string

const (
	JobRetryAdmissionEligible JobRetryAdmissionState = "eligible"
	JobRetryAdmissionBlocked  JobRetryAdmissionState = "blocked"
)

const (
	JobRetryBlockerSourceNotFailed       = "source_not_failed"
	JobRetryBlockerSourceAlreadyRetried  = "source_already_retried"
	JobRetryBlockerPolicyDenied          = "policy_denies_retry"
	JobRetryBlockerManualBudgetExhausted = "manual_retry_budget_exhausted"
	JobRetryBlockerFreshApprovalRequired = "fresh_approval_required"
)

// JobRetryPolicyEvidence is an immutable policy snapshot supplied by the
// authorization/policy layer. It contains no credential material and grants no
// execution authority by itself.
type JobRetryPolicyEvidence struct {
	PolicyID               string    `json:"policy_id"`
	PolicyDigest           string    `json:"policy_digest"`
	AllowsManualRetry      bool      `json:"allows_manual_retry"`
	UsedManualRetries      uint32    `json:"used_manual_retries"`
	MaxManualRetries       uint32    `json:"max_manual_retries"`
	RequiresFreshApproval  bool      `json:"requires_fresh_approval"`
	ApprovalEvidenceDigest string    `json:"approval_evidence_digest,omitempty"`
	ObservedAt             time.Time `json:"observed_at"`
}

// JobRetryAdmissionInput binds an operator retry decision to the exact durable
// Job version, immutable Change revision and exact policy/history evidence that
// were reviewed. ExpectedJobVersion is deliberately separate from Job.Version
// so a stale operator view fails closed.
type JobRetryAdmissionInput struct {
	Job                    job.Job
	ExpectedJobVersion     uint64
	RevisionID             string
	RevisionDigest         string
	Policy                 JobRetryPolicyEvidence
	RetryHistoryDigest     string
	RetryHistoryObservedAt time.Time
	SourceAlreadyRetried   bool
	ObservedAt             time.Time
}

// JobRetryAdmissionEvidence is a bounded, privacy-safe retry preflight. It is
// non-authorizing: Eligible means only that the reviewed evidence does not
// contain a known blocker. A mutation endpoint must re-read and revalidate the
// exact Job version, lineage/history and policy before creating any new retry
// lineage.
type JobRetryAdmissionEvidence struct {
	ContractVersion        string                 `json:"contract_version"`
	AdmissionID            string                 `json:"admission_id"`
	JobID                  string                 `json:"job_id"`
	JobVersion             uint64                 `json:"job_version"`
	ChangeID               string                 `json:"change_id"`
	ActionName             string                 `json:"action_name"`
	RevisionID             string                 `json:"revision_id"`
	RevisionDigest         string                 `json:"revision_digest"`
	SourceStatus           job.Status             `json:"source_status"`
	SourceAttempt          int                    `json:"source_attempt"`
	SourceMaxAttempts      int                    `json:"source_max_attempts"`
	SourceAlreadyRetried   bool                   `json:"source_already_retried"`
	PolicyID               string                 `json:"policy_id"`
	PolicyDigest           string                 `json:"policy_digest"`
	RetryHistoryDigest     string                 `json:"retry_history_digest"`
	RetryHistoryObservedAt time.Time              `json:"retry_history_observed_at"`
	UsedManualRetries      uint32                 `json:"used_manual_retries"`
	MaxManualRetries       uint32                 `json:"max_manual_retries"`
	RequiresFreshApproval  bool                   `json:"requires_fresh_approval"`
	ApprovalEvidenceDigest string                 `json:"approval_evidence_digest,omitempty"`
	State                  JobRetryAdmissionState `json:"state"`
	Blockers               []string               `json:"blockers"`
	ObservedAt             time.Time              `json:"observed_at"`
}

// BuildJobRetryAdmissionEvidence builds deterministic review evidence without
// mutating the source Job. Raw input, output, LastError, lease tokens and other
// sensitive execution details are intentionally excluded from the projection.
func BuildJobRetryAdmissionEvidence(input JobRetryAdmissionInput) (JobRetryAdmissionEvidence, error) {
	source := input.Job
	if err := validateRetryIdentity("job id", source.ID); err != nil {
		return JobRetryAdmissionEvidence{}, err
	}
	if err := validateRetryIdentity("change id", source.ChangeID); err != nil {
		return JobRetryAdmissionEvidence{}, err
	}
	if err := validateRetryIdentity("action name", source.ActionName); err != nil {
		return JobRetryAdmissionEvidence{}, err
	}
	if err := validateRetryIdentity("revision id", input.RevisionID); err != nil {
		return JobRetryAdmissionEvidence{}, err
	}
	if !validRetryDigest(input.RevisionDigest) {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: canonical revision digest is required", ErrInvalidJobRetryAdmission)
	}
	if source.Version == 0 || input.ExpectedJobVersion == 0 {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: positive durable Job version is required", ErrInvalidJobRetryAdmission)
	}
	if source.Version != input.ExpectedJobVersion {
		return JobRetryAdmissionEvidence{}, job.ErrVersionConflict
	}
	if source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() || source.UpdatedAt.Before(source.CreatedAt) {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: complete Job timestamps are required", ErrInvalidJobRetryAdmission)
	}
	if source.Attempt < 0 || source.MaxAttempts < 1 || source.Attempt > source.MaxAttempts {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: invalid Job attempt state", ErrInvalidJobRetryAdmission)
	}
	if source.Status == job.StatusFailed && source.Attempt < source.MaxAttempts {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: failed Job retains automatic retry budget", ErrInvalidJobRetryAdmission)
	}
	if source.Status.Terminal() && source.Lease != nil {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: terminal Job must not retain a lease", ErrInvalidJobRetryAdmission)
	}
	if err := validateRetryPolicy(input.Policy); err != nil {
		return JobRetryAdmissionEvidence{}, err
	}
	if !validRetryDigest(input.RetryHistoryDigest) {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: canonical retry history digest is required", ErrInvalidJobRetryAdmission)
	}
	if input.RetryHistoryObservedAt.IsZero() {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: retry history observation time is required", ErrInvalidJobRetryAdmission)
	}
	if input.Policy.ObservedAt.Before(source.UpdatedAt) || input.RetryHistoryObservedAt.Before(source.UpdatedAt) {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: retry policy/history evidence predates source Job state", ErrInvalidJobRetryAdmission)
	}
	if input.ObservedAt.IsZero() {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: observation time is required", ErrInvalidJobRetryAdmission)
	}
	observedAt := input.ObservedAt.UTC()
	if source.UpdatedAt.After(observedAt) || input.Policy.ObservedAt.After(observedAt) || input.RetryHistoryObservedAt.After(observedAt) {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: source evidence is newer than admission observation", ErrInvalidJobRetryAdmission)
	}

	blockers := make([]string, 0, 5)
	if source.Status != job.StatusFailed {
		blockers = append(blockers, JobRetryBlockerSourceNotFailed)
	}
	if input.SourceAlreadyRetried {
		blockers = append(blockers, JobRetryBlockerSourceAlreadyRetried)
	}
	if !input.Policy.AllowsManualRetry {
		blockers = append(blockers, JobRetryBlockerPolicyDenied)
	}
	if input.Policy.MaxManualRetries == 0 || input.Policy.UsedManualRetries >= input.Policy.MaxManualRetries {
		blockers = append(blockers, JobRetryBlockerManualBudgetExhausted)
	}
	if input.Policy.RequiresFreshApproval && input.Policy.ApprovalEvidenceDigest == "" {
		blockers = append(blockers, JobRetryBlockerFreshApprovalRequired)
	}
	blockers = canonicalRetryBlockers(blockers)
	if len(blockers) > MaxJobRetryAdmissionBlockers {
		return JobRetryAdmissionEvidence{}, fmt.Errorf("%w: blocker set exceeds bounded review surface", ErrInvalidJobRetryAdmission)
	}

	state := JobRetryAdmissionEligible
	if len(blockers) != 0 {
		state = JobRetryAdmissionBlocked
	}
	evidence := JobRetryAdmissionEvidence{
		ContractVersion:        JobRetryAdmissionContractVersion,
		JobID:                  source.ID,
		JobVersion:             source.Version,
		ChangeID:               source.ChangeID,
		ActionName:             source.ActionName,
		RevisionID:             input.RevisionID,
		RevisionDigest:         input.RevisionDigest,
		SourceStatus:           source.Status,
		SourceAttempt:          source.Attempt,
		SourceMaxAttempts:      source.MaxAttempts,
		SourceAlreadyRetried:   input.SourceAlreadyRetried,
		PolicyID:               input.Policy.PolicyID,
		PolicyDigest:           input.Policy.PolicyDigest,
		RetryHistoryDigest:     input.RetryHistoryDigest,
		RetryHistoryObservedAt: input.RetryHistoryObservedAt.UTC(),
		UsedManualRetries:      input.Policy.UsedManualRetries,
		MaxManualRetries:       input.Policy.MaxManualRetries,
		RequiresFreshApproval:  input.Policy.RequiresFreshApproval,
		ApprovalEvidenceDigest: input.Policy.ApprovalEvidenceDigest,
		State:                  state,
		Blockers:               blockers,
		ObservedAt:             observedAt,
	}
	admissionID, err := retryAdmissionDigest(evidence)
	if err != nil {
		return JobRetryAdmissionEvidence{}, err
	}
	evidence.AdmissionID = admissionID
	return evidence, nil
}

func validateRetryPolicy(policy JobRetryPolicyEvidence) error {
	if err := validateRetryIdentity("policy id", policy.PolicyID); err != nil {
		return err
	}
	if !validRetryDigest(policy.PolicyDigest) {
		return fmt.Errorf("%w: canonical policy digest is required", ErrInvalidJobRetryAdmission)
	}
	if policy.ObservedAt.IsZero() {
		return fmt.Errorf("%w: policy observation time is required", ErrInvalidJobRetryAdmission)
	}
	if policy.UsedManualRetries > policy.MaxManualRetries {
		return fmt.Errorf("%w: used manual retries exceed policy budget", ErrInvalidJobRetryAdmission)
	}
	if policy.ApprovalEvidenceDigest != "" && !validRetryDigest(policy.ApprovalEvidenceDigest) {
		return fmt.Errorf("%w: invalid approval evidence digest", ErrInvalidJobRetryAdmission)
	}
	return nil
}

func canonicalRetryBlockers(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func retryAdmissionDigest(evidence JobRetryAdmissionEvidence) (string, error) {
	copy := evidence
	copy.AdmissionID = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("%w: encode admission evidence: %v", ErrInvalidJobRetryAdmission, err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateRetryIdentity(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(value) > MaxJobRetryAdmissionIdentifierLength {
		return fmt.Errorf("%w: canonical %s is required", ErrInvalidJobRetryAdmission, name)
	}
	return nil
}

func validRetryDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, ch := range value[len("sha256:"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
