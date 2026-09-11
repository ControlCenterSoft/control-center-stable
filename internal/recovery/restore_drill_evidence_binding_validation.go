package recovery

import "fmt"

// ValidateRestoreDrillEvidenceBindingCurrent revalidates a persisted evidence
// binding against the exact current restore metadata and freshness assessment.
// It is side-effect free and grants no restore or production mutation authority.
func ValidateRestoreDrillEvidenceBindingCurrent(
	restore RestoreMetadata,
	assessment RestoreDrillFreshnessAssessment,
	binding RestoreDrillEvidenceBinding,
) error {
	if binding.SchemaVersion == "" ||
		binding.BindingID == "" ||
		binding.AssessmentID == "" ||
		binding.RestoreID == "" ||
		binding.RestoreResourceVersion == "" ||
		binding.VerificationEvidenceDigest == "" ||
		binding.VerifiedAt.IsZero() {
		return fmt.Errorf("restore drill evidence binding validation: persisted binding is incomplete")
	}

	expected, err := BuildRestoreDrillEvidenceBinding(restore, assessment)
	if err != nil {
		return fmt.Errorf("restore drill evidence binding validation: current source state is invalid or stale: %w", err)
	}

	if binding.SchemaVersion != expected.SchemaVersion ||
		binding.BindingID != expected.BindingID ||
		binding.AssessmentID != expected.AssessmentID ||
		binding.RestoreID != expected.RestoreID ||
		binding.RestoreResourceVersion != expected.RestoreResourceVersion ||
		binding.RestoreGeneration != expected.RestoreGeneration ||
		binding.VerificationEvidenceDigest != expected.VerificationEvidenceDigest ||
		!binding.VerifiedAt.Equal(expected.VerifiedAt) ||
		binding.AdvisoryOnly != expected.AdvisoryOnly ||
		binding.ProductionMutation != expected.ProductionMutation {
		return fmt.Errorf("restore drill evidence binding validation: persisted binding does not match exact current restore/assessment state")
	}

	return nil
}
