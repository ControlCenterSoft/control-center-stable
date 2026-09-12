package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaxManualRetryIdentifierLength = 255

var (
	ErrManualRetrySourceNotFailed      = errors.New("manual retry source job is not retryable")
	ErrManualRetrySourceAlreadyRetried = errors.New("manual retry source job version already has a retry lineage")
	ErrManualRetryAdmissionConflict    = errors.New("manual retry admission already represents different lineage")
	ErrManualRetryLineageNotFound      = errors.New("manual retry lineage not found")
)

// ManualRetryRequest is the durable mutation contract for creating a fresh Job
// lineage from one exact failed Job version. It intentionally contains only
// identifiers and digests from reviewed/revalidated evidence; raw Job input,
// output, LastError, lease data, credentials and secrets are never projected
// through this request.
type ManualRetryRequest struct {
	SourceJobID             string
	ExpectedSourceVersion   uint64
	RetryJobID              string
	RetryIdempotencyKey     string
	ReviewedAdmissionID     string
	RevalidationAdmissionID string
	RevisionID              string
	RevisionDigest          string
	PolicyID                string
	PolicyDigest            string
	RetryHistoryDigest      string
	ApprovalEvidenceDigest  string
	RequestedAt             time.Time
}

// ManualRetryLineage is immutable evidence that one reviewed admission created
// one fresh queued Job while preserving the failed source Job unchanged. The
// child idempotency key is retained only for internal replay matching and is
// deliberately excluded from JSON/evidence projection.
type ManualRetryLineage struct {
	RootJobID               string    `json:"root_job_id"`
	SourceJobID             string    `json:"source_job_id"`
	SourceJobVersion        uint64    `json:"source_job_version"`
	RetryJobID              string    `json:"retry_job_id"`
	RetryIdempotencyKey     string    `json:"-"`
	ReviewedAdmissionID     string    `json:"reviewed_admission_id"`
	RevalidationAdmissionID string    `json:"revalidation_admission_id"`
	RevisionID              string    `json:"revision_id"`
	RevisionDigest          string    `json:"revision_digest"`
	PolicyID                string    `json:"policy_id"`
	PolicyDigest            string    `json:"policy_digest"`
	RetryHistoryDigest      string    `json:"retry_history_digest"`
	ApprovalEvidenceDigest  string    `json:"approval_evidence_digest,omitempty"`
	RequestedAt             time.Time `json:"requested_at"`
}

// ManualRetryRepository atomically binds a reviewed failed Job version to one
// fresh Job lineage. Implementations must reject stale source versions, reject a
// second lineage from the same exact failed source version, and make exact
// replay idempotent without creating a second child Job.
type ManualRetryRepository interface {
	Get(context.Context, string) (Job, error)
	CreateManualRetry(context.Context, ManualRetryRequest) (retry Job, lineage ManualRetryLineage, created bool, err error)
	GetManualRetryLineageByAdmission(context.Context, string) (ManualRetryLineage, error)
	ListManualRetryLineage(context.Context, string) ([]ManualRetryLineage, error)
}

// ValidateManualRetryRequest validates only the bounded lineage contract. The
// authorization/policy layer must still revalidate the reviewed admission
// immediately before calling CreateManualRetry.
func ValidateManualRetryRequest(request ManualRetryRequest) error {
	identifiers := []struct {
		name  string
		value string
	}{
		{"source job id", request.SourceJobID},
		{"retry job id", request.RetryJobID},
		{"retry idempotency key", request.RetryIdempotencyKey},
		{"revision id", request.RevisionID},
		{"policy id", request.PolicyID},
	}
	for _, identifier := range identifiers {
		if err := validateManualRetryIdentifier(identifier.name, identifier.value); err != nil {
			return err
		}
	}
	if request.ExpectedSourceVersion == 0 {
		return errors.New("expected source job version is required")
	}
	if request.SourceJobID == request.RetryJobID {
		return errors.New("retry job must have a fresh job id")
	}
	digests := []struct {
		name  string
		value string
	}{
		{"reviewed admission id", request.ReviewedAdmissionID},
		{"revalidation admission id", request.RevalidationAdmissionID},
		{"revision digest", request.RevisionDigest},
		{"policy digest", request.PolicyDigest},
		{"retry history digest", request.RetryHistoryDigest},
	}
	for _, digest := range digests {
		if !validManualRetryDigest(digest.value) {
			return fmt.Errorf("canonical %s is required", digest.name)
		}
	}
	if request.ApprovalEvidenceDigest != "" && !validManualRetryDigest(request.ApprovalEvidenceDigest) {
		return errors.New("canonical approval evidence digest is required")
	}
	if request.RequestedAt.IsZero() {
		return errors.New("manual retry request time is required")
	}
	return nil
}

// ManualRetryRequestFingerprint is stable across transport retries. RequestedAt
// and RevalidationAdmissionID are deliberately excluded: a replay may perform a
// later fresh revalidation while representing the same operator-reviewed
// mutation. Semantic revision/policy/history/approval digests remain bound.
func ManualRetryRequestFingerprint(request ManualRetryRequest) (string, error) {
	if err := ValidateManualRetryRequest(request); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, value := range []string{
		request.SourceJobID,
		fmt.Sprintf("%d", request.ExpectedSourceVersion),
		request.RetryJobID,
		request.RetryIdempotencyKey,
		request.ReviewedAdmissionID,
		request.RevisionID,
		request.RevisionDigest,
		request.PolicyID,
		request.PolicyDigest,
		request.RetryHistoryDigest,
		request.ApprovalEvidenceDigest,
	} {
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func validateManualRetryIdentifier(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(value) > MaxManualRetryIdentifierLength {
		return fmt.Errorf("canonical %s is required", name)
	}
	return nil
}

func validManualRetryDigest(value string) bool {
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
