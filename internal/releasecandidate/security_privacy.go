package releasecandidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	SecurityPrivacyEvidenceSchemaV1    = "control-center.release-security-privacy-evidence.v1"
	SecurityPrivacyDispositionApproved = "approved"
)

// SecurityPrivacyEvidence is a bounded, exact-candidate release-readiness
// contract. It records disposition/evidence identity only; it does not contain
// secrets, raw Job input/output, personal data, provider payloads or logs.
type SecurityPrivacyEvidence struct {
	Schema                         string `json:"schema"`
	CandidateVersion               string `json:"candidate_version"`
	CandidateSHA                   string `json:"candidate_sha"`
	Disposition                    string `json:"disposition"`
	AuditEvidenceDigest            string `json:"audit_evidence_digest"`
	NoSecretBoundaryReviewed       bool   `json:"no_secret_boundary_reviewed"`
	RBACScopesReviewed             bool   `json:"rbac_scopes_reviewed"`
	StaleReplayIdempotencyReviewed bool   `json:"stale_replay_idempotency_reviewed"`
	AuditIntegrityReviewed         bool   `json:"audit_integrity_reviewed"`
	RecoverySemanticsReviewed      bool   `json:"recovery_semantics_reviewed"`
	DataMinimizationReviewed       bool   `json:"data_minimization_reviewed"`
	ErrorEvidenceRedactionReviewed bool   `json:"error_evidence_redaction_reviewed"`
	ProductionAuthorityUnchanged   bool   `json:"production_authority_unchanged"`
	CriticalSecurityFindingsClosed bool   `json:"critical_security_findings_closed"`
	CriticalPrivacyFindingsClosed  bool   `json:"critical_privacy_findings_closed"`
	SnapshotDigest                 string `json:"snapshot_digest"`
}

// SecurityPrivacySnapshotDigest returns a deterministic digest for all bounded
// security/privacy disposition fields except SnapshotDigest itself. The digest
// binds a future gate result to one exact candidate and one external evidence
// set; it is not a substitute for the underlying security/privacy review.
func SecurityPrivacySnapshotDigest(evidence SecurityPrivacyEvidence) string {
	payload := struct {
		Schema                         string `json:"schema"`
		CandidateVersion               string `json:"candidate_version"`
		CandidateSHA                   string `json:"candidate_sha"`
		Disposition                    string `json:"disposition"`
		AuditEvidenceDigest            string `json:"audit_evidence_digest"`
		NoSecretBoundaryReviewed       bool   `json:"no_secret_boundary_reviewed"`
		RBACScopesReviewed             bool   `json:"rbac_scopes_reviewed"`
		StaleReplayIdempotencyReviewed bool   `json:"stale_replay_idempotency_reviewed"`
		AuditIntegrityReviewed         bool   `json:"audit_integrity_reviewed"`
		RecoverySemanticsReviewed      bool   `json:"recovery_semantics_reviewed"`
		DataMinimizationReviewed       bool   `json:"data_minimization_reviewed"`
		ErrorEvidenceRedactionReviewed bool   `json:"error_evidence_redaction_reviewed"`
		ProductionAuthorityUnchanged   bool   `json:"production_authority_unchanged"`
		CriticalSecurityFindingsClosed bool   `json:"critical_security_findings_closed"`
		CriticalPrivacyFindingsClosed  bool   `json:"critical_privacy_findings_closed"`
	}{
		Schema:                         strings.TrimSpace(evidence.Schema),
		CandidateVersion:               strings.TrimSpace(evidence.CandidateVersion),
		CandidateSHA:                   strings.TrimSpace(evidence.CandidateSHA),
		Disposition:                    strings.ToLower(strings.TrimSpace(evidence.Disposition)),
		AuditEvidenceDigest:            strings.TrimSpace(evidence.AuditEvidenceDigest),
		NoSecretBoundaryReviewed:       evidence.NoSecretBoundaryReviewed,
		RBACScopesReviewed:             evidence.RBACScopesReviewed,
		StaleReplayIdempotencyReviewed: evidence.StaleReplayIdempotencyReviewed,
		AuditIntegrityReviewed:         evidence.AuditIntegrityReviewed,
		RecoverySemanticsReviewed:      evidence.RecoverySemanticsReviewed,
		DataMinimizationReviewed:       evidence.DataMinimizationReviewed,
		ErrorEvidenceRedactionReviewed: evidence.ErrorEvidenceRedactionReviewed,
		ProductionAuthorityUnchanged:   evidence.ProductionAuthorityUnchanged,
		CriticalSecurityFindingsClosed: evidence.CriticalSecurityFindingsClosed,
		CriticalPrivacyFindingsClosed:  evidence.CriticalPrivacyFindingsClosed,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal security/privacy evidence: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EvaluateSecurityPrivacyEvidence validates and projects the bounded final
// security/privacy disposition into the generic 0.31 readiness gate. Unknown,
// incomplete or internally inconsistent evidence fails closed. This function
// does not run scanners/tests, trigger CI, mutate a release, grant production
// authority or claim Release Candidate/Public Stable status.
func EvaluateSecurityPrivacyEvidence(evidence SecurityPrivacyEvidence) (GateEvidence, []string, error) {
	if strings.TrimSpace(evidence.Schema) != SecurityPrivacyEvidenceSchemaV1 {
		return GateEvidence{}, nil, fmt.Errorf("unsupported security/privacy evidence schema %q", evidence.Schema)
	}
	if strings.TrimSpace(evidence.CandidateVersion) != CandidateVersion {
		return GateEvidence{}, nil, fmt.Errorf("unexpected candidate version %q", evidence.CandidateVersion)
	}
	if !shaRE.MatchString(strings.TrimSpace(evidence.CandidateSHA)) {
		return GateEvidence{}, nil, fmt.Errorf("invalid candidate sha")
	}
	if !digestRE.MatchString(strings.TrimSpace(evidence.AuditEvidenceDigest)) {
		return GateEvidence{}, nil, fmt.Errorf("invalid security/privacy audit evidence digest")
	}
	if !digestRE.MatchString(strings.TrimSpace(evidence.SnapshotDigest)) {
		return GateEvidence{}, nil, fmt.Errorf("invalid security/privacy snapshot digest")
	}
	if evidence.SnapshotDigest != SecurityPrivacySnapshotDigest(evidence) {
		return GateEvidence{}, nil, fmt.Errorf("security/privacy snapshot digest mismatch")
	}

	blockers := make([]string, 0, 11)
	if strings.ToLower(strings.TrimSpace(evidence.Disposition)) != SecurityPrivacyDispositionApproved {
		blockers = append(blockers, "security_privacy_disposition")
	}
	if !evidence.NoSecretBoundaryReviewed {
		blockers = append(blockers, "no_secret_boundary")
	}
	if !evidence.RBACScopesReviewed {
		blockers = append(blockers, "rbac_scopes")
	}
	if !evidence.StaleReplayIdempotencyReviewed {
		blockers = append(blockers, "stale_replay_idempotency")
	}
	if !evidence.AuditIntegrityReviewed {
		blockers = append(blockers, "audit_integrity")
	}
	if !evidence.RecoverySemanticsReviewed {
		blockers = append(blockers, "recovery_semantics")
	}
	if !evidence.DataMinimizationReviewed {
		blockers = append(blockers, "data_minimization")
	}
	if !evidence.ErrorEvidenceRedactionReviewed {
		blockers = append(blockers, "error_evidence_redaction")
	}
	if !evidence.ProductionAuthorityUnchanged {
		blockers = append(blockers, "production_authority")
	}
	if !evidence.CriticalSecurityFindingsClosed {
		blockers = append(blockers, "critical_security_findings")
	}
	if !evidence.CriticalPrivacyFindingsClosed {
		blockers = append(blockers, "critical_privacy_findings")
	}

	status := GateBlocked
	if len(blockers) == 0 {
		status = GatePass
	}
	gate := GateEvidence{
		Gate:           GateSecurityPrivacy,
		Status:         status,
		CandidateSHA:   evidence.CandidateSHA,
		EvidenceDigest: evidence.SnapshotDigest,
	}
	return gate, blockers, nil
}
