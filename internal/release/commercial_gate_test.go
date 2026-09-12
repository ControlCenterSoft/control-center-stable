package release

import (
	"strings"
	"testing"
)

func TestEvaluateCommercialGateApproved(t *testing.T) {
	blockers := EvaluateCommercialGate(approvedCommercialEvidence("0.31.0", strings.Repeat("c", 40)))
	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}
}

func TestEvaluateCommercialGateApprovedLabelAloneFailsClosed(t *testing.T) {
	blockers := EvaluateCommercialGate(CommercialEvidence{Disposition: CommercialDispositionApproved})
	want := []string{
		"commercial_evidence",
		"third_party_dependencies",
		"redistribution",
		"third_party_notices",
		"source_obligations",
		"sbom",
		"legal_terms",
		"release_claims",
	}
	if len(blockers) != len(want) {
		t.Fatalf("blockers=%#v want=%#v", blockers, want)
	}
	for i := range want {
		if blockers[i] != want[i] {
			t.Fatalf("blockers=%#v want=%#v", blockers, want)
		}
	}
}

func TestEvaluateCommercialGateRejectsMalformedDigest(t *testing.T) {
	evidence := approvedCommercialEvidence("0.31.0", strings.Repeat("c", 40))
	evidence.EvidenceDigest = "sha256:" + strings.Repeat("G", 64)
	blockers := EvaluateCommercialGate(evidence)
	if len(blockers) != 1 || blockers[0] != "commercial_evidence" {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}
}

func TestEvaluateCommercialGateDispositionIsCaseAndSpaceNormalized(t *testing.T) {
	evidence := approvedCommercialEvidence("0.31.0", strings.Repeat("c", 40))
	evidence.Disposition = "  APPROVED  "
	blockers := EvaluateCommercialGate(evidence)
	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}
}

func TestEvaluateCommercialGateBlockedDispositionCannotPromote(t *testing.T) {
	evidence := approvedCommercialEvidence("0.31.0", strings.Repeat("c", 40))
	evidence.Disposition = "blocked"
	blockers := EvaluateCommercialGate(evidence)
	if len(blockers) != 1 || blockers[0] != "commercial_disposition" {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}
}
