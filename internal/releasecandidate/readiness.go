package releasecandidate

import (
	"fmt"
	"regexp"
)

const (
	SchemaV1             = "control-center.release-candidate-readiness.v1"
	StableVersion        = "0.30.0"
	StableTag            = "v0.30.0"
	StableArtifactDigest = "sha256:02d15e8ff13bbcb52b6d0c9293ab8804500991fbb41c8b575e88306a8a5ce0f2"
	CandidateVersion     = "0.31.0"
)

type GateID string

const (
	GateManualRetryLineage GateID = "manual_retry_lineage_integration"
	GateOperationalE2E     GateID = "operational_workflow_e2e"
	GatePackaging          GateID = "candidate_artifact_packaging"
	GateCleanInstall       GateID = "clean_install"
	GateUpgradeFromStable  GateID = "upgrade_from_stable_0_30"
	GateRollbackRecovery   GateID = "rollback_forward_recovery"
	GatePostgresRestart    GateID = "postgres_restart_reconnect"
	GateSecurityPrivacy    GateID = "security_privacy"
	GateCommercialLegal    GateID = "commercial_legal_clearance"
	GateReleaseMetadata    GateID = "release_metadata"
)

var requiredGates = [...]GateID{
	GateManualRetryLineage,
	GateOperationalE2E,
	GatePackaging,
	GateCleanInstall,
	GateUpgradeFromStable,
	GateRollbackRecovery,
	GatePostgresRestart,
	GateSecurityPrivacy,
	GateCommercialLegal,
	GateReleaseMetadata,
}

// productStableRequiredGates contains the product-release gates required by the
// canonical Public Stable policy. Commercial/legal clearance is deliberately a
// separate commercial-launch track: an incomplete commercial package must not
// weaken technical safety, but it does not block publication of a technically
// qualified product release.
var productStableRequiredGates = [...]GateID{
	GateManualRetryLineage,
	GateOperationalE2E,
	GatePackaging,
	GateCleanInstall,
	GateUpgradeFromStable,
	GateRollbackRecovery,
	GatePostgresRestart,
	GateSecurityPrivacy,
	GateReleaseMetadata,
}

// RequiredGates returns a defensive copy of the full commercial-readiness gate
// set so callers cannot weaken that policy by mutating package-level state.
func RequiredGates() []GateID {
	return append([]GateID(nil), requiredGates[:]...)
}

// ProductStableRequiredGates returns a defensive copy of the technical
// product-release gate set used for Public Stable publication.
func ProductStableRequiredGates() []GateID {
	return append([]GateID(nil), productStableRequiredGates[:]...)
}

type GateStatus string

const (
	GatePending GateStatus = "pending"
	GateBlocked GateStatus = "blocked"
	GatePass    GateStatus = "pass"
)

type GateEvidence struct {
	Gate           GateID     `json:"gate"`
	Status         GateStatus `json:"status"`
	CandidateSHA   string     `json:"candidate_sha"`
	EvidenceDigest string     `json:"evidence_digest,omitempty"`
}

type Snapshot struct {
	Schema               string         `json:"schema"`
	StableVersion        string         `json:"stable_version"`
	StableTag            string         `json:"stable_tag"`
	StableArtifactDigest string         `json:"stable_artifact_digest"`
	CandidateVersion     string         `json:"candidate_version"`
	CandidateSHA         string         `json:"candidate_sha"`
	Gates                []GateEvidence `json:"gates"`
}

type Result struct {
	Ready    bool     `json:"ready"`
	Blockers []GateID `json:"blockers"`
}

var (
	shaRE    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func validateSnapshot(snapshot Snapshot) (map[GateID]GateEvidence, error) {
	if snapshot.Schema != SchemaV1 {
		return nil, fmt.Errorf("unsupported readiness schema %q", snapshot.Schema)
	}
	if snapshot.StableVersion != StableVersion || snapshot.StableTag != StableTag {
		return nil, fmt.Errorf("unexpected stable identity %q (%q)", snapshot.StableVersion, snapshot.StableTag)
	}
	if snapshot.StableArtifactDigest != StableArtifactDigest {
		return nil, fmt.Errorf("unexpected stable artifact digest")
	}
	if snapshot.CandidateVersion != CandidateVersion {
		return nil, fmt.Errorf("unexpected candidate version %q", snapshot.CandidateVersion)
	}
	if !shaRE.MatchString(snapshot.CandidateSHA) {
		return nil, fmt.Errorf("invalid candidate sha")
	}

	known := make(map[GateID]struct{}, len(requiredGates))
	for _, gate := range requiredGates {
		known[gate] = struct{}{}
	}

	seen := make(map[GateID]GateEvidence, len(snapshot.Gates))
	for _, evidence := range snapshot.Gates {
		if _, ok := known[evidence.Gate]; !ok {
			return nil, fmt.Errorf("unknown release gate %q", evidence.Gate)
		}
		if _, duplicate := seen[evidence.Gate]; duplicate {
			return nil, fmt.Errorf("duplicate release gate %q", evidence.Gate)
		}
		if evidence.CandidateSHA != snapshot.CandidateSHA {
			return nil, fmt.Errorf("release gate %q is bound to a different candidate sha", evidence.Gate)
		}
		switch evidence.Status {
		case GatePass:
			if !digestRE.MatchString(evidence.EvidenceDigest) {
				return nil, fmt.Errorf("release gate %q pass is missing bounded evidence digest", evidence.Gate)
			}
		case GatePending, GateBlocked:
			if evidence.EvidenceDigest != "" && !digestRE.MatchString(evidence.EvidenceDigest) {
				return nil, fmt.Errorf("release gate %q has invalid evidence digest", evidence.Gate)
			}
		default:
			return nil, fmt.Errorf("release gate %q has invalid status %q", evidence.Gate, evidence.Status)
		}
		seen[evidence.Gate] = evidence
	}
	return seen, nil
}

func evaluateRequired(snapshot Snapshot, required []GateID) (Result, error) {
	seen, err := validateSnapshot(snapshot)
	if err != nil {
		return Result{}, err
	}

	result := Result{}
	for _, gate := range required {
		evidence, ok := seen[gate]
		if !ok || evidence.Status != GatePass {
			result.Blockers = append(result.Blockers, gate)
		}
	}
	result.Ready = len(result.Blockers) == 0
	return result, nil
}

// Evaluate aggregates the complete commercial-readiness gate set. It does not
// execute checks, build artifacts, mutate a release, trigger CI, deploy
// software or grant publication authority.
func Evaluate(snapshot Snapshot) (Result, error) {
	return evaluateRequired(snapshot, requiredGates[:])
}

// EvaluateProductStable evaluates the canonical Public Stable product-release
// policy. Commercial/legal evidence is still validated when present and may be
// reported separately, but only technical correctness gates determine whether
// the product is ready for Public Stable publication.
func EvaluateProductStable(snapshot Snapshot) (Result, error) {
	return evaluateRequired(snapshot, productStableRequiredGates[:])
}
