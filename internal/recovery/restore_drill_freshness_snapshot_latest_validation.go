package recovery

import (
	"fmt"
	"time"
)

// ValidateRestoreDrillFreshnessSnapshotLatestCurrent revalidates a persisted
// freshness snapshot against the authoritative current restore set for its
// exact target. This closes the gap where an individually valid old snapshot
// could remain self-consistent after newer successful restore-drill evidence
// appears. It is read-only and grants no recovery or production authority.
func ValidateRestoreDrillFreshnessSnapshotLatestCurrent(
	restores []RestoreMetadata,
	snapshot RestoreDrillFreshnessSnapshot,
) error {
	if err := ValidateRestoreDrillFreshnessSnapshot(snapshot); err != nil {
		return fmt.Errorf("restore drill freshness latest-current validation: invalid snapshot: %w", err)
	}

	expected, err := ReadLatestRestoreDrillFreshnessSnapshot(
		restores,
		snapshot.Target,
		time.Duration(snapshot.MaxAgeSeconds)*time.Second,
		snapshot.CheckedAt,
	)
	if err != nil {
		return fmt.Errorf("restore drill freshness latest-current validation: current restore set is invalid: %w", err)
	}
	if !sameRestoreDrillFreshnessSnapshot(expected, snapshot) {
		return fmt.Errorf("restore drill freshness latest-current validation: persisted snapshot is not the latest exact current restore evidence")
	}
	return nil
}
