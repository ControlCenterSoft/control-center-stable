package recovery

import (
	"fmt"
	"time"
)

// ValidateRestoreDrillFreshnessSnapshotCurrent revalidates a persisted or
// API-facing freshness snapshot against one exact current restore record.
// It is side-effect free and grants no restore or production mutation authority.
//
// UNVERIFIABLE snapshots are intentionally rejected here: a single restore
// record cannot prove that no other valid restore-drill evidence exists, and an
// unverifiable snapshot carries no exact restore lineage that can be rebound.
func ValidateRestoreDrillFreshnessSnapshotCurrent(
	restore RestoreMetadata,
	snapshot RestoreDrillFreshnessSnapshot,
) error {
	if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err != nil {
		return fmt.Errorf("restore drill freshness snapshot current validation: invalid snapshot: %w", err)
	}
	if snapshot.State == RestoreDrillSnapshotUnverifiable {
		return fmt.Errorf("restore drill freshness snapshot current validation: unverifiable snapshot has no exact restore lineage")
	}

	expected, err := ReadLatestRestoreDrillFreshnessSnapshot(
		[]RestoreMetadata{restore},
		snapshot.Target,
		time.Duration(snapshot.MaxAgeSeconds)*time.Second,
		snapshot.CheckedAt,
	)
	if err != nil {
		return fmt.Errorf("restore drill freshness snapshot current validation: current restore state is invalid: %w", err)
	}
	if expected.State == RestoreDrillSnapshotUnverifiable {
		return fmt.Errorf("restore drill freshness snapshot current validation: current restore is not valid freshness evidence for the exact target")
	}
	if !sameRestoreDrillFreshnessSnapshot(expected, snapshot) {
		return fmt.Errorf("restore drill freshness snapshot current validation: persisted snapshot does not match exact current restore evidence")
	}
	return nil
}

func sameRestoreDrillFreshnessSnapshot(left, right RestoreDrillFreshnessSnapshot) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.Target == right.Target &&
		left.State == right.State &&
		left.RestoreID == right.RestoreID &&
		left.RestoreResourceVersion == right.RestoreResourceVersion &&
		left.RestoreGeneration == right.RestoreGeneration &&
		left.AssessmentID == right.AssessmentID &&
		sameOptionalTime(left.VerifiedAt, right.VerifiedAt) &&
		left.CheckedAt.Equal(right.CheckedAt) &&
		sameOptionalTime(left.ValidUntil, right.ValidUntil) &&
		left.MaxAgeSeconds == right.MaxAgeSeconds &&
		left.VerificationEvidenceDigest == right.VerificationEvidenceDigest &&
		left.Reason == right.Reason &&
		left.AdvisoryOnly == right.AdvisoryOnly &&
		left.ProductionMutation == right.ProductionMutation
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
