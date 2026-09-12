package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	ManualRetryHistoryContractVersion = "job.manual-retry-history/v1"
	MaxManualRetryHistoryEntries      = 64
)

var ErrInvalidManualRetryHistory = errors.New("invalid manual retry history")

// ManualRetryLineageReader is the read-only boundary used to reconstruct an
// authoritative retry chain. It intentionally has no mutation authority.
type ManualRetryLineageReader interface {
	GetManualRetryLineageByRetryJob(context.Context, string) (ManualRetryLineage, error)
	ListManualRetryLineage(context.Context, string) ([]ManualRetryLineage, error)
}

type ManualRetryHistoryEvidence struct {
	ContractVersion      string    `json:"contract_version"`
	RootJobID            string    `json:"root_job_id"`
	SourceJobID          string    `json:"source_job_id"`
	SourceJobVersion     uint64    `json:"source_job_version"`
	UsedManualRetries    uint32    `json:"used_manual_retries"`
	SourceAlreadyRetried bool      `json:"source_already_retried"`
	Digest               string    `json:"digest"`
	ObservedAt           time.Time `json:"observed_at"`
}

type manualRetryHistoryDigestPayload struct {
	ContractVersion string                    `json:"contract_version"`
	RootJobID       string                    `json:"root_job_id"`
	Entries         []manualRetryHistoryEntry `json:"entries"`
}

type manualRetryHistoryEntry struct {
	SourceJobID             string    `json:"source_job_id"`
	SourceJobVersion        uint64    `json:"source_job_version"`
	RetryJobID              string    `json:"retry_job_id"`
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

// BuildManualRetryHistoryEvidence reconstructs the retry chain from immutable
// lineage records, validates that it is a single connected chain and hashes a
// privacy-safe canonical projection. ObservedAt is the time the durable history
// was read; it is deliberately separate from the timestamps of historical
// retry events so a failed child Job can be evaluated with fresh evidence.
func BuildManualRetryHistoryEvidence(ctx context.Context, reader ManualRetryLineageReader, source Job, observedAt time.Time) (ManualRetryHistoryEvidence, error) {
	if reader == nil {
		return ManualRetryHistoryEvidence{}, errors.New("manual retry lineage reader is required")
	}
	if err := validateManualRetryIdentifier("source job id", source.ID); err != nil {
		return ManualRetryHistoryEvidence{}, err
	}
	if source.Version == 0 {
		return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: positive source Job version is required", ErrInvalidManualRetryHistory)
	}
	if observedAt.IsZero() || source.UpdatedAt.IsZero() || observedAt.Before(source.UpdatedAt) {
		return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: fresh history observation is required", ErrInvalidManualRetryHistory)
	}

	rootJobID := source.ID
	parent, err := reader.GetManualRetryLineageByRetryJob(ctx, source.ID)
	if err == nil {
		rootJobID = parent.RootJobID
	} else if !errors.Is(err, ErrManualRetryLineageNotFound) {
		return ManualRetryHistoryEvidence{}, err
	}
	if err := validateManualRetryIdentifier("root job id", rootJobID); err != nil {
		return ManualRetryHistoryEvidence{}, err
	}

	lineage, err := reader.ListManualRetryLineage(ctx, rootJobID)
	if err != nil {
		return ManualRetryHistoryEvidence{}, err
	}
	if len(lineage) > MaxManualRetryHistoryEntries {
		return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: retry history exceeds bounded evidence size", ErrInvalidManualRetryHistory)
	}

	bySource := make(map[string]ManualRetryLineage, len(lineage))
	for _, item := range lineage {
		if item.RootJobID != rootJobID || item.SourceJobID == "" || item.RetryJobID == "" || item.SourceJobVersion == 0 || item.RequestedAt.IsZero() {
			return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: incomplete retry lineage entry", ErrInvalidManualRetryHistory)
		}
		if _, exists := bySource[item.SourceJobID]; exists {
			return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: retry lineage forks at source Job %q", ErrInvalidManualRetryHistory, item.SourceJobID)
		}
		bySource[item.SourceJobID] = item
	}

	entries := make([]manualRetryHistoryEntry, 0, len(lineage))
	visited := make(map[string]struct{}, len(lineage)+1)
	cursor := rootJobID
	for {
		if _, seen := visited[cursor]; seen {
			return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: retry lineage cycle detected", ErrInvalidManualRetryHistory)
		}
		visited[cursor] = struct{}{}
		item, exists := bySource[cursor]
		if !exists {
			break
		}
		entries = append(entries, manualRetryHistoryEntry{
			SourceJobID:             item.SourceJobID,
			SourceJobVersion:        item.SourceJobVersion,
			RetryJobID:              item.RetryJobID,
			ReviewedAdmissionID:     item.ReviewedAdmissionID,
			RevalidationAdmissionID: item.RevalidationAdmissionID,
			RevisionID:              item.RevisionID,
			RevisionDigest:          item.RevisionDigest,
			PolicyID:                item.PolicyID,
			PolicyDigest:            item.PolicyDigest,
			RetryHistoryDigest:      item.RetryHistoryDigest,
			ApprovalEvidenceDigest:  item.ApprovalEvidenceDigest,
			RequestedAt:             item.RequestedAt.UTC(),
		})
		cursor = item.RetryJobID
	}
	if len(entries) != len(lineage) {
		return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: retry lineage contains disconnected entries", ErrInvalidManualRetryHistory)
	}

	payload := manualRetryHistoryDigestPayload{
		ContractVersion: ManualRetryHistoryContractVersion,
		RootJobID:       rootJobID,
		Entries:         entries,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ManualRetryHistoryEvidence{}, fmt.Errorf("%w: encode retry history: %v", ErrInvalidManualRetryHistory, err)
	}
	sum := sha256.Sum256(encoded)
	_, sourceAlreadyRetried := bySource[source.ID]
	return ManualRetryHistoryEvidence{
		ContractVersion:      ManualRetryHistoryContractVersion,
		RootJobID:            rootJobID,
		SourceJobID:          source.ID,
		SourceJobVersion:     source.Version,
		UsedManualRetries:    uint32(len(entries)),
		SourceAlreadyRetried: sourceAlreadyRetried,
		Digest:               "sha256:" + hex.EncodeToString(sum[:]),
		ObservedAt:           observedAt.UTC(),
	}, nil
}
