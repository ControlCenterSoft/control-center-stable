package release

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// PromotionEvidence captures release evidence required before channel promotion.
type PromotionEvidence struct {
	Version              string
	Revision             string
	TestsPassed          bool
	QualificationBinding ReleaseEvidenceBinding
	SecurityPassed       bool
	SecurityBinding      ReleaseEvidenceBinding
	RollbackPrepared     bool
	RollbackBinding      ReleaseEvidenceBinding
	Artifact             ArtifactEvidence
	Commercial           CommercialEvidence
}

// PromotionDecision describes whether an artifact may enter a target channel.
type PromotionDecision struct {
	Allowed  bool
	Blockers []string
}

// EvaluatePromotionGate applies deterministic evidence requirements per channel.
func EvaluatePromotionGate(targetChannel string, evidence PromotionEvidence) (PromotionDecision, error) {
	channel := strings.ToLower(strings.TrimSpace(targetChannel))
	if channel != "development" && channel != "candidate" && channel != "stable" {
		return PromotionDecision{}, fmt.Errorf("unsupported target channel %q", targetChannel)
	}
	if strings.TrimSpace(evidence.Version) == "" {
		return PromotionDecision{}, fmt.Errorf("version is required")
	}

	blockers := make([]string, 0, 25)
	if !evidence.TestsPassed {
		blockers = append(blockers, "tests")
	}
	if !evidence.SecurityPassed {
		blockers = append(blockers, "security")
	}
	if channel == "candidate" || channel == "stable" {
		if !validReleaseRevision(evidence.Revision) {
			blockers = append(blockers, "revision")
		}
		if evidence.TestsPassed && !evidence.QualificationBinding.matches(evidence.Version, evidence.Revision) {
			blockers = append(blockers, "tests_binding")
		}
		if evidence.SecurityPassed && !evidence.SecurityBinding.matches(evidence.Version, evidence.Revision) {
			blockers = append(blockers, "security_binding")
		}
		if !evidence.RollbackPrepared {
			blockers = append(blockers, "rollback")
		} else if !evidence.RollbackBinding.matches(evidence.Version, evidence.Revision) {
			blockers = append(blockers, "rollback_binding")
		}
		if !evidence.Artifact.Binding.matches(evidence.Version, evidence.Revision) {
			blockers = append(blockers, "artifact_binding")
		}
		blockers = append(blockers, EvaluateArtifactGate(channel, evidence.Artifact)...)
		if !evidence.Commercial.Binding.matches(evidence.Version, evidence.Revision) {
			blockers = append(blockers, "commercial_binding")
		}
		blockers = append(blockers, EvaluateCommercialGate(evidence.Commercial)...)
	}
	return PromotionDecision{Allowed: len(blockers) == 0, Blockers: blockers}, nil
}

func validReleaseRevision(value string) bool {
	value = strings.TrimSpace(value)
	if (len(value) != 40 && len(value) != 64) || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
