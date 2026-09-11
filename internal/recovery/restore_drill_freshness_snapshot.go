package recovery

import (
	"fmt"
	"time"
)

const RestoreDrillFreshnessSnapshotSchemaVersion = "recovery.restore-drill-freshness-snapshot/v1"

type RestoreDrillFreshnessSnapshotState string

const (
	RestoreDrillSnapshotFresh        RestoreDrillFreshnessSnapshotState = "FRESH"
	RestoreDrillSnapshotStale        RestoreDrillFreshnessSnapshotState = "STALE"
	RestoreDrillSnapshotUnverifiable RestoreDrillFreshnessSnapshotState = "UNVERIFIABLE"
)

// RestoreDrillFreshnessSnapshot is a read-only projection of the newest valid
// successful isolated restore-drill evidence for one exact target. It does not
// describe the newest restore attempt and grants no restore or mutation authority.
type RestoreDrillFreshnessSnapshot struct {
	SchemaVersion              string                             `json:"schema_version"`
	Target                     ObjectReference                    `json:"target"`
	State                      RestoreDrillFreshnessSnapshotState `json:"state"`
	RestoreID                  string                             `json:"restore_id,omitempty"`
	RestoreResourceVersion     string                             `json:"restore_resource_version,omitempty"`
	RestoreGeneration          uint64                             `json:"restore_generation,omitempty"`
	AssessmentID               string                             `json:"assessment_id,omitempty"`
	VerifiedAt                 *time.Time                         `json:"verified_at,omitempty"`
	CheckedAt                  time.Time                          `json:"checked_at"`
	ValidUntil                 *time.Time                         `json:"valid_until,omitempty"`
	MaxAgeSeconds              uint64                             `json:"max_age_seconds"`
	VerificationEvidenceDigest string                             `json:"verification_evidence_digest,omitempty"`
	Reason                     string                             `json:"reason,omitempty"`
	AdvisoryOnly               bool                               `json:"advisory_only"`
	ProductionMutation         bool                               `json:"production_mutation"`
}

// ReadLatestRestoreDrillFreshnessSnapshot selects the most recently verified
// valid successful isolated drill for target, then evaluates freshness using
// the canonical 0.27 freshness contract. Equal verification timestamps are
// resolved deterministically by the lexicographically smaller restore ID.
// Invalid, failed, non-drill and non-isolated records are not freshness evidence.
func ReadLatestRestoreDrillFreshnessSnapshot(
	restores []RestoreMetadata,
	target ObjectReference,
	maxAge time.Duration,
	checkedAt time.Time,
) (RestoreDrillFreshnessSnapshot, error) {
	if err := validateObjectReference("target", target); err != nil {
		return RestoreDrillFreshnessSnapshot{}, fmt.Errorf("restore drill freshness snapshot: invalid target: %w", err)
	}
	if maxAge <= 0 || maxAge > maxRestoreDrillFreshnessWindow || maxAge%time.Second != 0 {
		return RestoreDrillFreshnessSnapshot{}, fmt.Errorf("restore drill freshness snapshot: max age must be whole seconds within (0,365d]")
	}
	if checkedAt.IsZero() {
		return RestoreDrillFreshnessSnapshot{}, fmt.Errorf("restore drill freshness snapshot: checked_at is required")
	}
	checkedAt = checkedAt.UTC().Truncate(time.Second)

	base := RestoreDrillFreshnessSnapshot{
		SchemaVersion:      RestoreDrillFreshnessSnapshotSchemaVersion,
		Target:             target,
		State:              RestoreDrillSnapshotUnverifiable,
		CheckedAt:          checkedAt,
		MaxAgeSeconds:      uint64(maxAge / time.Second),
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}

	var selected *RestoreMetadata
	var selectedVerifiedAt time.Time
	for i := range restores {
		restore := &restores[i]
		if restore.Target != target {
			continue
		}
		if err := ValidateRestoreMetadata(*restore); err != nil {
			continue
		}
		if restore.Mode != RestoreIsolatedDrill ||
			restore.State != RestoreSucceeded ||
			restore.Verification.Outcome != VerificationPassed ||
			restore.Verification.VerifiedAt == nil {
			continue
		}

		verifiedAt := restore.Verification.VerifiedAt.UTC().Truncate(time.Second)
		if checkedAt.Before(verifiedAt) {
			continue
		}
		if selected == nil || verifiedAt.After(selectedVerifiedAt) ||
			(verifiedAt.Equal(selectedVerifiedAt) && restore.ObjectID < selected.ObjectID) {
			selected = restore
			selectedVerifiedAt = verifiedAt
		}
	}

	if selected == nil {
		base.Reason = "no valid successful verified isolated restore drill evidence for exact target"
		return base, nil
	}

	assessment, err := EvaluateRestoreDrillFreshness(*selected, maxAge, checkedAt)
	if err != nil {
		return RestoreDrillFreshnessSnapshot{}, fmt.Errorf("restore drill freshness snapshot: evaluate selected restore: %w", err)
	}
	evidenceDigest, err := digestRestoreVerificationEvidence(selected.Verification.Evidence)
	if err != nil {
		return RestoreDrillFreshnessSnapshot{}, fmt.Errorf("restore drill freshness snapshot: digest verification evidence: %w", err)
	}

	verifiedAt := assessment.VerifiedAt.UTC()
	validUntil := assessment.ValidUntil.UTC()
	base.RestoreID = selected.ObjectID
	base.RestoreResourceVersion = selected.ResourceVersion
	base.RestoreGeneration = selected.Generation
	base.AssessmentID = assessment.AssessmentID
	base.VerifiedAt = &verifiedAt
	base.ValidUntil = &validUntil
	base.VerificationEvidenceDigest = evidenceDigest
	base.Reason = ""
	if assessment.State == RestoreDrillFresh {
		base.State = RestoreDrillSnapshotFresh
	} else {
		base.State = RestoreDrillSnapshotStale
	}
	return base, nil
}
