package recovery

import (
	"fmt"
	"strings"
	"time"
)

// ValidateRestoreDrillFreshnessSnapshot validates a stored or API-facing
// snapshot without granting any recovery authority. Invalid, inconsistent or
// authority-bearing projections are rejected fail-closed.
func ValidateRestoreDrillFreshnessSnapshot(snapshot RestoreDrillFreshnessSnapshot) error {
	if snapshot.SchemaVersion != RestoreDrillFreshnessSnapshotSchemaVersion {
		return fmt.Errorf("restore drill freshness snapshot: schema_version must be %s", RestoreDrillFreshnessSnapshotSchemaVersion)
	}
	if err := validateObjectReference("target", snapshot.Target); err != nil {
		return fmt.Errorf("restore drill freshness snapshot: invalid target: %w", err)
	}
	if snapshot.MaxAgeSeconds == 0 || snapshot.MaxAgeSeconds > uint64(maxRestoreDrillFreshnessWindow/time.Second) {
		return fmt.Errorf("restore drill freshness snapshot: max_age_seconds outside supported window")
	}
	if snapshot.CheckedAt.IsZero() {
		return fmt.Errorf("restore drill freshness snapshot: checked_at is required")
	}
	if !snapshot.CheckedAt.Equal(snapshot.CheckedAt.UTC().Truncate(time.Second)) {
		return fmt.Errorf("restore drill freshness snapshot: checked_at must have whole-second precision")
	}
	if !snapshot.AdvisoryOnly || snapshot.ProductionMutation {
		return fmt.Errorf("restore drill freshness snapshot: unsafe authority flags")
	}

	switch snapshot.State {
	case RestoreDrillSnapshotUnverifiable:
		if strings.TrimSpace(snapshot.Reason) == "" || len(snapshot.Reason) > 512 {
			return fmt.Errorf("restore drill freshness snapshot: unverifiable state requires bounded reason")
		}
		if snapshot.RestoreID != "" || snapshot.RestoreResourceVersion != "" || snapshot.RestoreGeneration != 0 ||
			snapshot.AssessmentID != "" || snapshot.VerifiedAt != nil || snapshot.ValidUntil != nil ||
			snapshot.VerificationEvidenceDigest != "" {
			return fmt.Errorf("restore drill freshness snapshot: unverifiable state must not expose fabricated lineage")
		}
		return nil

	case RestoreDrillSnapshotFresh, RestoreDrillSnapshotStale:
		if snapshot.Reason != "" {
			return fmt.Errorf("restore drill freshness snapshot: verified state must not carry unverifiable reason")
		}
		if !identifierPattern.MatchString(snapshot.RestoreID) {
			return fmt.Errorf("restore drill freshness snapshot: restore_id must be a canonical identifier")
		}
		if !identifierPattern.MatchString(snapshot.RestoreResourceVersion) {
			return fmt.Errorf("restore drill freshness snapshot: restore_resource_version must be canonical")
		}
		if snapshot.RestoreGeneration == 0 {
			return fmt.Errorf("restore drill freshness snapshot: restore_generation must be positive")
		}
		if !identifierPattern.MatchString(snapshot.AssessmentID) || !strings.HasPrefix(snapshot.AssessmentID, "rdf-") {
			return fmt.Errorf("restore drill freshness snapshot: assessment_id must be canonical restore freshness identity")
		}
		if !digestPattern.MatchString(snapshot.VerificationEvidenceDigest) {
			return fmt.Errorf("restore drill freshness snapshot: invalid verification_evidence_digest")
		}
		if snapshot.VerifiedAt == nil || snapshot.ValidUntil == nil {
			return fmt.Errorf("restore drill freshness snapshot: verified state requires verified_at and valid_until")
		}
		verifiedAt := snapshot.VerifiedAt.UTC()
		validUntil := snapshot.ValidUntil.UTC()
		if verifiedAt.IsZero() || validUntil.IsZero() {
			return fmt.Errorf("restore drill freshness snapshot: verified timestamps must not be zero")
		}
		if !verifiedAt.Equal(verifiedAt.Truncate(time.Second)) || !validUntil.Equal(validUntil.Truncate(time.Second)) {
			return fmt.Errorf("restore drill freshness snapshot: verified timestamps must have whole-second precision")
		}
		if snapshot.CheckedAt.Before(verifiedAt) {
			return fmt.Errorf("restore drill freshness snapshot: checked_at precedes verified_at")
		}
		expectedValidUntil := verifiedAt.Add(time.Duration(snapshot.MaxAgeSeconds) * time.Second)
		if !validUntil.Equal(expectedValidUntil) {
			return fmt.Errorf("restore drill freshness snapshot: valid_until does not match verified_at plus max_age_seconds")
		}
		expectedState := RestoreDrillSnapshotFresh
		assessmentState := RestoreDrillFresh
		if !snapshot.CheckedAt.Before(validUntil) {
			expectedState = RestoreDrillSnapshotStale
			assessmentState = RestoreDrillStale
		}
		if snapshot.State != expectedState {
			return fmt.Errorf("restore drill freshness snapshot: state does not match checked_at freshness boundary")
		}

		expectedAssessmentID, err := restoreDrillFreshnessAssessmentIdentity(RestoreDrillFreshnessAssessment{
			SchemaVersion:      RestoreDrillFreshnessSchemaVersion,
			RestoreID:          snapshot.RestoreID,
			Target:             snapshot.Target,
			VerifiedAt:         verifiedAt,
			CheckedAt:          snapshot.CheckedAt.UTC(),
			ValidUntil:         validUntil,
			MaxAgeSeconds:      snapshot.MaxAgeSeconds,
			State:              assessmentState,
			AdvisoryOnly:       snapshot.AdvisoryOnly,
			ProductionMutation: snapshot.ProductionMutation,
		})
		if err != nil {
			return fmt.Errorf("restore drill freshness snapshot: calculate assessment identity: %w", err)
		}
		if snapshot.AssessmentID != expectedAssessmentID {
			return fmt.Errorf("restore drill freshness snapshot: assessment_id does not match freshness claim")
		}
		return nil

	default:
		return fmt.Errorf("restore drill freshness snapshot: unsupported state %q", snapshot.State)
	}
}
