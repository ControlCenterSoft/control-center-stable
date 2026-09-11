package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const RestoreDrillFreshnessSchemaVersion = "recovery.restore-drill-freshness/v1"

const maxRestoreDrillFreshnessWindow = 365 * 24 * time.Hour

type RestoreDrillFreshnessState string

const (
	RestoreDrillFresh RestoreDrillFreshnessState = "FRESH"
	RestoreDrillStale RestoreDrillFreshnessState = "STALE"
)

// RestoreDrillFreshnessAssessment is read-only evidence that describes whether
// a successfully verified isolated restore drill is still recent enough for a
// caller-selected policy window. It grants no restore or production authority.
type RestoreDrillFreshnessAssessment struct {
	SchemaVersion      string                     `json:"schema_version"`
	AssessmentID       string                     `json:"assessment_id"`
	RestoreID          string                     `json:"restore_id"`
	Target             ObjectReference            `json:"target"`
	VerifiedAt         time.Time                  `json:"verified_at"`
	CheckedAt          time.Time                  `json:"checked_at"`
	ValidUntil         time.Time                  `json:"valid_until"`
	MaxAgeSeconds      uint64                     `json:"max_age_seconds"`
	State              RestoreDrillFreshnessState `json:"state"`
	AdvisoryOnly       bool                       `json:"advisory_only"`
	ProductionMutation bool                       `json:"production_mutation"`
}

func EvaluateRestoreDrillFreshness(restore RestoreMetadata, maxAge time.Duration, checkedAt time.Time) (RestoreDrillFreshnessAssessment, error) {
	if err := ValidateRestoreMetadata(restore); err != nil {
		return RestoreDrillFreshnessAssessment{}, fmt.Errorf("restore drill freshness: invalid restore metadata: %w", err)
	}
	if restore.Mode != RestoreIsolatedDrill || restore.State != RestoreSucceeded || restore.Verification.Outcome != VerificationPassed || restore.Verification.VerifiedAt == nil {
		return RestoreDrillFreshnessAssessment{}, fmt.Errorf("restore drill freshness: a successful verified isolated drill is required")
	}
	if maxAge <= 0 || maxAge > maxRestoreDrillFreshnessWindow || maxAge%time.Second != 0 {
		return RestoreDrillFreshnessAssessment{}, fmt.Errorf("restore drill freshness: max age must be whole seconds within (0,365d]")
	}
	if checkedAt.IsZero() {
		return RestoreDrillFreshnessAssessment{}, fmt.Errorf("restore drill freshness: checked_at is required")
	}

	verifiedAt := restore.Verification.VerifiedAt.UTC().Truncate(time.Second)
	checkedAt = checkedAt.UTC().Truncate(time.Second)
	if checkedAt.Before(verifiedAt) {
		return RestoreDrillFreshnessAssessment{}, fmt.Errorf("restore drill freshness: checked_at precedes verified_at")
	}
	validUntil := verifiedAt.Add(maxAge)
	state := RestoreDrillFresh
	if !checkedAt.Before(validUntil) {
		state = RestoreDrillStale
	}

	result := RestoreDrillFreshnessAssessment{
		SchemaVersion:      RestoreDrillFreshnessSchemaVersion,
		RestoreID:          restore.ObjectID,
		Target:             restore.Target,
		VerifiedAt:         verifiedAt,
		CheckedAt:          checkedAt,
		ValidUntil:         validUntil,
		MaxAgeSeconds:      uint64(maxAge / time.Second),
		State:              state,
		AdvisoryOnly:       true,
		ProductionMutation: false,
	}
	assessmentID, err := restoreDrillFreshnessAssessmentIdentity(result)
	if err != nil {
		return RestoreDrillFreshnessAssessment{}, fmt.Errorf("restore drill freshness: encode identity: %w", err)
	}
	result.AssessmentID = assessmentID
	return result, nil
}

// restoreDrillFreshnessAssessmentIdentity returns the deterministic identity
// for an assessment's complete non-authorizing freshness claim. Keeping this
// calculation shared by generation and validation prevents a stored/API
// projection from substituting a different canonical-looking assessment ID.
func restoreDrillFreshnessAssessmentIdentity(assessment RestoreDrillFreshnessAssessment) (string, error) {
	canonical := struct {
		SchemaVersion      string                     `json:"schema_version"`
		RestoreID          string                     `json:"restore_id"`
		Target             ObjectReference            `json:"target"`
		VerifiedAt         time.Time                  `json:"verified_at"`
		CheckedAt          time.Time                  `json:"checked_at"`
		ValidUntil         time.Time                  `json:"valid_until"`
		MaxAgeSeconds      uint64                     `json:"max_age_seconds"`
		State              RestoreDrillFreshnessState `json:"state"`
		AdvisoryOnly       bool                       `json:"advisory_only"`
		ProductionMutation bool                       `json:"production_mutation"`
	}{
		SchemaVersion:      assessment.SchemaVersion,
		RestoreID:          assessment.RestoreID,
		Target:             assessment.Target,
		VerifiedAt:         assessment.VerifiedAt.UTC(),
		CheckedAt:          assessment.CheckedAt.UTC(),
		ValidUntil:         assessment.ValidUntil.UTC(),
		MaxAgeSeconds:      assessment.MaxAgeSeconds,
		State:              assessment.State,
		AdvisoryOnly:       assessment.AdvisoryOnly,
		ProductionMutation: assessment.ProductionMutation,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "rdf-" + hex.EncodeToString(digest[:])[:24], nil
}
