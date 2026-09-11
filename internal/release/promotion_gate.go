package release

import (
	"fmt"
	"strings"
)

// PromotionEvidence captures release evidence required before channel promotion.
type PromotionEvidence struct {
	Version          string
	TestsPassed      bool
	SecurityPassed   bool
	ArtifactSigned   bool
	RollbackPrepared bool
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

	blockers := make([]string, 0, 4)
	if !evidence.TestsPassed {
		blockers = append(blockers, "tests")
	}
	if !evidence.SecurityPassed {
		blockers = append(blockers, "security")
	}
	if (channel == "candidate" || channel == "stable") && !evidence.ArtifactSigned {
		blockers = append(blockers, "signature")
	}
	if channel == "stable" && !evidence.RollbackPrepared {
		blockers = append(blockers, "rollback")
	}
	return PromotionDecision{Allowed: len(blockers) == 0, Blockers: blockers}, nil
}
