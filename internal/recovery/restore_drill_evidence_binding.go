package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const RestoreDrillEvidenceBindingSchemaVersion = "recovery.restore-drill-evidence-binding/v1"

// RestoreDrillEvidenceBinding binds a freshness assessment to the exact stored
// restore resource version and immutable verification evidence that produced it.
// The binding is read-only evidence and grants no restore or production authority.
type RestoreDrillEvidenceBinding struct {
	SchemaVersion              string    `json:"schema_version"`
	BindingID                  string    `json:"binding_id"`
	AssessmentID               string    `json:"assessment_id"`
	RestoreID                  string    `json:"restore_id"`
	RestoreResourceVersion     string    `json:"restore_resource_version"`
	RestoreGeneration          uint64    `json:"restore_generation"`
	VerificationEvidenceDigest string    `json:"verification_evidence_digest"`
	VerifiedAt                 time.Time `json:"verified_at"`
	AdvisoryOnly               bool      `json:"advisory_only"`
	ProductionMutation         bool      `json:"production_mutation"`
}

// BuildRestoreDrillEvidenceBinding proves that assessment is the deterministic
// freshness result for restore, then binds that result to the exact persisted
// restore version and verification evidence set. It does not dereference
// evidence, execute a restore, or mutate recovery state.
func BuildRestoreDrillEvidenceBinding(restore RestoreMetadata, assessment RestoreDrillFreshnessAssessment) (RestoreDrillEvidenceBinding, error) {
	if err := ValidateRestoreMetadata(restore); err != nil {
		return RestoreDrillEvidenceBinding{}, fmt.Errorf("restore drill evidence binding: invalid restore metadata: %w", err)
	}
	if assessment.MaxAgeSeconds == 0 || assessment.MaxAgeSeconds > uint64(maxRestoreDrillFreshnessWindow/time.Second) {
		return RestoreDrillEvidenceBinding{}, fmt.Errorf("restore drill evidence binding: invalid assessment max_age_seconds")
	}

	expected, err := EvaluateRestoreDrillFreshness(
		restore,
		time.Duration(assessment.MaxAgeSeconds)*time.Second,
		assessment.CheckedAt,
	)
	if err != nil {
		return RestoreDrillEvidenceBinding{}, fmt.Errorf("restore drill evidence binding: re-evaluate freshness: %w", err)
	}
	if !sameRestoreDrillFreshnessAssessment(expected, assessment) {
		return RestoreDrillEvidenceBinding{}, fmt.Errorf("restore drill evidence binding: assessment does not match exact restore state")
	}

	evidenceDigest, err := digestRestoreVerificationEvidence(restore.Verification.Evidence)
	if err != nil {
		return RestoreDrillEvidenceBinding{}, err
	}

	binding := RestoreDrillEvidenceBinding{
		SchemaVersion:              RestoreDrillEvidenceBindingSchemaVersion,
		AssessmentID:               assessment.AssessmentID,
		RestoreID:                  restore.ObjectID,
		RestoreResourceVersion:     restore.ResourceVersion,
		RestoreGeneration:          restore.Generation,
		VerificationEvidenceDigest: evidenceDigest,
		VerifiedAt:                 assessment.VerifiedAt.UTC(),
		AdvisoryOnly:               true,
		ProductionMutation:         false,
	}

	canonical := struct {
		SchemaVersion              string    `json:"schema_version"`
		AssessmentID               string    `json:"assessment_id"`
		RestoreID                  string    `json:"restore_id"`
		RestoreResourceVersion     string    `json:"restore_resource_version"`
		RestoreGeneration          uint64    `json:"restore_generation"`
		VerificationEvidenceDigest string    `json:"verification_evidence_digest"`
		VerifiedAt                 time.Time `json:"verified_at"`
		AdvisoryOnly               bool      `json:"advisory_only"`
		ProductionMutation         bool      `json:"production_mutation"`
	}{
		SchemaVersion:              binding.SchemaVersion,
		AssessmentID:               binding.AssessmentID,
		RestoreID:                  binding.RestoreID,
		RestoreResourceVersion:     binding.RestoreResourceVersion,
		RestoreGeneration:          binding.RestoreGeneration,
		VerificationEvidenceDigest: binding.VerificationEvidenceDigest,
		VerifiedAt:                 binding.VerifiedAt,
		AdvisoryOnly:               binding.AdvisoryOnly,
		ProductionMutation:         binding.ProductionMutation,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return RestoreDrillEvidenceBinding{}, fmt.Errorf("restore drill evidence binding: encode identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	binding.BindingID = "rdeb-" + hex.EncodeToString(digest[:])[:24]
	return binding, nil
}

func sameRestoreDrillFreshnessAssessment(left, right RestoreDrillFreshnessAssessment) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.AssessmentID == right.AssessmentID &&
		left.RestoreID == right.RestoreID &&
		left.Target == right.Target &&
		left.VerifiedAt.Equal(right.VerifiedAt) &&
		left.CheckedAt.Equal(right.CheckedAt) &&
		left.ValidUntil.Equal(right.ValidUntil) &&
		left.MaxAgeSeconds == right.MaxAgeSeconds &&
		left.State == right.State &&
		left.AdvisoryOnly == right.AdvisoryOnly &&
		left.ProductionMutation == right.ProductionMutation
}

func digestRestoreVerificationEvidence(evidence []EvidenceReference) (string, error) {
	type identity struct {
		ID         string       `json:"id"`
		Kind       EvidenceKind `json:"kind"`
		Reference  string       `json:"reference"`
		Digest     string       `json:"digest"`
		RecordedAt time.Time    `json:"recorded_at"`
	}

	canonical := make([]identity, 0, len(evidence))
	for _, item := range evidence {
		canonical = append(canonical, identity{
			ID: item.ID, Kind: item.Kind, Reference: item.Reference, Digest: item.Digest,
			RecordedAt: item.RecordedAt.UTC(),
		})
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].ID != canonical[j].ID {
			return canonical[i].ID < canonical[j].ID
		}
		if canonical[i].Kind != canonical[j].Kind {
			return canonical[i].Kind < canonical[j].Kind
		}
		if canonical[i].Reference != canonical[j].Reference {
			return canonical[i].Reference < canonical[j].Reference
		}
		if canonical[i].Digest != canonical[j].Digest {
			return canonical[i].Digest < canonical[j].Digest
		}
		return canonical[i].RecordedAt.Before(canonical[j].RecordedAt)
	})

	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("restore drill evidence binding: encode verification evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
