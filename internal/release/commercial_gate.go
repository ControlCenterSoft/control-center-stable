package release

import (
	"encoding/hex"
	"strings"
)

const CommercialDispositionApproved = "approved"

// CommercialEvidence contains only bounded release-readiness evidence. It does
// not carry legal text, customer data, credentials, dependency source code, or
// other material that should live in dedicated evidence stores/artifacts.
type CommercialEvidence struct {
	Binding                   ReleaseEvidenceBinding
	Disposition               string
	EvidenceDigest            string
	DependenciesReviewed      bool
	RedistributionReviewed    bool
	NoticesPrepared           bool
	SourceObligationsResolved bool
	SBOMPrepared              bool
	LegalTermsDispositioned   bool
	ReleaseClaimsReviewed     bool
}

// EvaluateCommercialGate returns deterministic blockers for commercial/legal
// release readiness. Unknown, missing, or partially reviewed evidence fails
// closed; an "approved" label alone is never sufficient.
func EvaluateCommercialGate(evidence CommercialEvidence) []string {
	blockers := make([]string, 0, 8)
	if strings.ToLower(strings.TrimSpace(evidence.Disposition)) != CommercialDispositionApproved {
		blockers = append(blockers, "commercial_disposition")
	}
	if !validSHA256EvidenceDigest(evidence.EvidenceDigest) {
		blockers = append(blockers, "commercial_evidence")
	}
	if !evidence.DependenciesReviewed {
		blockers = append(blockers, "third_party_dependencies")
	}
	if !evidence.RedistributionReviewed {
		blockers = append(blockers, "redistribution")
	}
	if !evidence.NoticesPrepared {
		blockers = append(blockers, "third_party_notices")
	}
	if !evidence.SourceObligationsResolved {
		blockers = append(blockers, "source_obligations")
	}
	if !evidence.SBOMPrepared {
		blockers = append(blockers, "sbom")
	}
	if !evidence.LegalTermsDispositioned {
		blockers = append(blockers, "legal_terms")
	}
	if !evidence.ReleaseClaimsReviewed {
		blockers = append(blockers, "release_claims")
	}
	return blockers
}

func validSHA256EvidenceDigest(value string) bool {
	const prefix = "sha256:"
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
