package releasecandidate

import (
	"strings"
	"testing"
)

func approvedSecurityPrivacyEvidence() SecurityPrivacyEvidence {
	evidence := SecurityPrivacyEvidence{
		Schema:                         SecurityPrivacyEvidenceSchemaV1,
		CandidateVersion:               CandidateVersion,
		CandidateSHA:                   strings.Repeat("c", 40),
		Disposition:                    SecurityPrivacyDispositionApproved,
		AuditEvidenceDigest:            "sha256:" + strings.Repeat("a", 64),
		NoSecretBoundaryReviewed:       true,
		RBACScopesReviewed:             true,
		StaleReplayIdempotencyReviewed: true,
		AuditIntegrityReviewed:         true,
		RecoverySemanticsReviewed:      true,
		DataMinimizationReviewed:       true,
		ErrorEvidenceRedactionReviewed: true,
		ProductionAuthorityUnchanged:   true,
		CriticalSecurityFindingsClosed: true,
		CriticalPrivacyFindingsClosed:  true,
	}
	evidence.SnapshotDigest = SecurityPrivacySnapshotDigest(evidence)
	return evidence
}

func TestEvaluateSecurityPrivacyEvidenceApproved(t *testing.T) {
	evidence := approvedSecurityPrivacyEvidence()
	gate, blockers, err := EvaluateSecurityPrivacyEvidence(evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", blockers)
	}
	if gate.Gate != GateSecurityPrivacy || gate.Status != GatePass {
		t.Fatalf("unexpected gate: %#v", gate)
	}
	if gate.CandidateSHA != evidence.CandidateSHA || gate.EvidenceDigest != evidence.SnapshotDigest {
		t.Fatalf("gate binding mismatch: %#v", gate)
	}
}

func TestEvaluateSecurityPrivacyEvidenceApprovedLabelAloneFailsClosed(t *testing.T) {
	evidence := approvedSecurityPrivacyEvidence()
	evidence.NoSecretBoundaryReviewed = false
	evidence.RBACScopesReviewed = false
	evidence.StaleReplayIdempotencyReviewed = false
	evidence.AuditIntegrityReviewed = false
	evidence.RecoverySemanticsReviewed = false
	evidence.DataMinimizationReviewed = false
	evidence.ErrorEvidenceRedactionReviewed = false
	evidence.ProductionAuthorityUnchanged = false
	evidence.CriticalSecurityFindingsClosed = false
	evidence.CriticalPrivacyFindingsClosed = false
	evidence.SnapshotDigest = SecurityPrivacySnapshotDigest(evidence)

	gate, blockers, err := EvaluateSecurityPrivacyEvidence(evidence)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gate.Status != GateBlocked {
		t.Fatalf("gate status=%q want=%q", gate.Status, GateBlocked)
	}
	want := []string{
		"no_secret_boundary",
		"rbac_scopes",
		"stale_replay_idempotency",
		"audit_integrity",
		"recovery_semantics",
		"data_minimization",
		"error_evidence_redaction",
		"production_authority",
		"critical_security_findings",
		"critical_privacy_findings",
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

func TestEvaluateSecurityPrivacyEvidenceRejectsTamperedSnapshot(t *testing.T) {
	evidence := approvedSecurityPrivacyEvidence()
	evidence.RecoverySemanticsReviewed = false
	if _, _, err := EvaluateSecurityPrivacyEvidence(evidence); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
}

func TestEvaluateSecurityPrivacyEvidenceRejectsMalformedAuditDigest(t *testing.T) {
	evidence := approvedSecurityPrivacyEvidence()
	evidence.AuditEvidenceDigest = "sha256:" + strings.Repeat("G", 64)
	evidence.SnapshotDigest = SecurityPrivacySnapshotDigest(evidence)
	if _, _, err := EvaluateSecurityPrivacyEvidence(evidence); err == nil || !strings.Contains(err.Error(), "audit evidence digest") {
		t.Fatalf("expected audit evidence digest error, got %v", err)
	}
}

func TestEvaluateSecurityPrivacyEvidenceRejectsWrongCandidate(t *testing.T) {
	evidence := approvedSecurityPrivacyEvidence()
	evidence.CandidateVersion = "0.32.0"
	evidence.SnapshotDigest = SecurityPrivacySnapshotDigest(evidence)
	if _, _, err := EvaluateSecurityPrivacyEvidence(evidence); err == nil || !strings.Contains(err.Error(), "candidate version") {
		t.Fatalf("expected candidate version error, got %v", err)
	}
}

func TestSecurityPrivacySnapshotDigestNormalizesDisposition(t *testing.T) {
	evidence := approvedSecurityPrivacyEvidence()
	want := evidence.SnapshotDigest
	evidence.Disposition = "  APPROVED  "
	if got := SecurityPrivacySnapshotDigest(evidence); got != want {
		t.Fatalf("digest=%q want=%q", got, want)
	}
}
