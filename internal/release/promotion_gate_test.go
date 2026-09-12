package release

import (
	"strings"
	"testing"
)

func releaseBinding(version, revision string) ReleaseEvidenceBinding {
	return ReleaseEvidenceBinding{Version: version, Revision: revision}
}

func approvedCommercialEvidence(version, revision string) CommercialEvidence {
	return CommercialEvidence{
		Binding:                   releaseBinding(version, revision),
		Disposition:               CommercialDispositionApproved,
		EvidenceDigest:            "sha256:" + strings.Repeat("a", 64),
		DependenciesReviewed:      true,
		RedistributionReviewed:    true,
		NoticesPrepared:           true,
		SourceObligationsResolved: true,
		SBOMPrepared:              true,
		LegalTermsDispositioned:   true,
		ReleaseClaimsReviewed:     true,
	}
}

func candidateArtifactEvidence(version, revision string) ArtifactEvidence {
	return ArtifactEvidence{
		Binding:               releaseBinding(version, revision),
		BinaryDigest:          "sha256:" + strings.Repeat("1", 64),
		ChecksumSidecar:       true,
		QualificationManifest: true,
		Provenance:            true,
	}
}

func stableArtifactEvidence(version, revision string) ArtifactEvidence {
	evidence := candidateArtifactEvidence(version, revision)
	evidence.SourceDigest = "sha256:" + strings.Repeat("2", 64)
	evidence.SHA256SUMS = true
	evidence.ReleaseManifest = true
	return evidence
}

func candidatePromotionEvidence(version, revision string) PromotionEvidence {
	binding := releaseBinding(version, revision)
	return PromotionEvidence{
		Version:              version,
		Revision:             revision,
		TestsPassed:          true,
		QualificationBinding: binding,
		SecurityPassed:       true,
		SecurityBinding:      binding,
		RollbackPrepared:     true,
		RollbackBinding:      binding,
		Artifact:             candidateArtifactEvidence(version, revision),
		Commercial:           approvedCommercialEvidence(version, revision),
	}
}

func TestEvaluatePromotionGateStable(t *testing.T) {
	version := "0.31.0"
	revision := strings.Repeat("b", 40)
	evidence := candidatePromotionEvidence(version, revision)
	evidence.Artifact = stableArtifactEvidence(version, revision)

	decision, err := EvaluatePromotionGate("stable", evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed || len(decision.Blockers) != 0 {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluatePromotionGateStableReportsBlockers(t *testing.T) {
	decision, err := EvaluatePromotionGate("stable", PromotionEvidence{Version: "0.31.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"tests",
		"security",
		"revision",
		"rollback",
		"artifact_binding",
		"artifact_digest",
		"checksum_sidecar",
		"qualification_manifest",
		"provenance",
		"source_artifact_digest",
		"sha256sums",
		"release_manifest",
		"commercial_binding",
		"commercial_disposition",
		"commercial_evidence",
		"third_party_dependencies",
		"redistribution",
		"third_party_notices",
		"source_obligations",
		"sbom",
		"legal_terms",
		"release_claims",
	}
	if len(decision.Blockers) != len(want) {
		t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
	}
	for i := range want {
		if decision.Blockers[i] != want[i] {
			t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
		}
	}
}

func TestEvaluatePromotionGateCandidateRequiresRollback(t *testing.T) {
	version := "0.31.0"
	revision := strings.Repeat("c", 40)
	evidence := candidatePromotionEvidence(version, revision)
	evidence.RollbackPrepared = false
	evidence.RollbackBinding = ReleaseEvidenceBinding{}

	decision, err := EvaluatePromotionGate("candidate", evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Allowed || len(decision.Blockers) != 1 || decision.Blockers[0] != "rollback" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluatePromotionGateDevelopmentDoesNotRequireReleaseEvidence(t *testing.T) {
	decision, err := EvaluatePromotionGate("development", PromotionEvidence{Version: "0.31.0-dev", TestsPassed: true, SecurityPassed: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("unexpected blockers: %#v", decision.Blockers)
	}
}

func TestEvaluatePromotionGateRejectsUnboundRevision(t *testing.T) {
	version := "0.31.0"
	revision := "latest"
	evidence := candidatePromotionEvidence(version, revision)

	decision, err := EvaluatePromotionGate("candidate", evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"revision", "tests_binding", "security_binding", "rollback_binding", "artifact_binding", "commercial_binding"}
	if decision.Allowed || len(decision.Blockers) != len(want) {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	for i := range want {
		if decision.Blockers[i] != want[i] {
			t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
		}
	}
}

func TestEvaluatePromotionGateRejectsCrossRevisionEvidence(t *testing.T) {
	version := "0.31.0"
	revision := strings.Repeat("c", 40)
	other := strings.Repeat("d", 40)
	evidence := candidatePromotionEvidence(version, revision)
	evidence.QualificationBinding = releaseBinding(version, other)
	evidence.SecurityBinding = releaseBinding(version, other)
	evidence.RollbackBinding = releaseBinding(version, other)
	evidence.Artifact.Binding = releaseBinding(version, other)
	evidence.Commercial.Binding = releaseBinding(version, other)

	decision, err := EvaluatePromotionGate("candidate", evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"tests_binding", "security_binding", "rollback_binding", "artifact_binding", "commercial_binding"}
	if decision.Allowed || len(decision.Blockers) != len(want) {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	for i := range want {
		if decision.Blockers[i] != want[i] {
			t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
		}
	}
}

func TestEvaluatePromotionGateRejectsCrossVersionEvidence(t *testing.T) {
	version := "0.31.0"
	revision := strings.Repeat("c", 40)
	evidence := candidatePromotionEvidence(version, revision)
	evidence.Artifact.Binding = releaseBinding("0.32.0", revision)
	evidence.Commercial.Binding = releaseBinding("0.32.0", revision)

	decision, err := EvaluatePromotionGate("candidate", evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"artifact_binding", "commercial_binding"}
	if decision.Allowed || len(decision.Blockers) != len(want) {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	for i := range want {
		if decision.Blockers[i] != want[i] {
			t.Fatalf("blockers=%#v want=%#v", decision.Blockers, want)
		}
	}
}
