package release

import "strings"

// ArtifactEvidence captures bounded integrity/publication evidence for one exact
// candidate artifact. Control Center currently uses SHA-256 release identity and
// provenance; a detached cryptographic signature is not implied by this model.
type ArtifactEvidence struct {
	Binding               ReleaseEvidenceBinding
	BinaryDigest          string
	SourceDigest          string
	ChecksumSidecar       bool
	SHA256SUMS            bool
	QualificationManifest bool
	ReleaseManifest       bool
	Provenance            bool
}

// EvaluateArtifactGate returns deterministic packaging blockers for candidate
// and stable promotion. It intentionally does not treat a checksum as a digital
// signature and does not claim signature support when none is published.
func EvaluateArtifactGate(targetChannel string, evidence ArtifactEvidence) []string {
	channel := strings.ToLower(strings.TrimSpace(targetChannel))
	if channel != "candidate" && channel != "stable" {
		return nil
	}

	blockers := make([]string, 0, 7)
	if !validSHA256EvidenceDigest(evidence.BinaryDigest) {
		blockers = append(blockers, "artifact_digest")
	}
	if !evidence.ChecksumSidecar {
		blockers = append(blockers, "checksum_sidecar")
	}
	if !evidence.QualificationManifest {
		blockers = append(blockers, "qualification_manifest")
	}
	if !evidence.Provenance {
		blockers = append(blockers, "provenance")
	}
	if channel == "stable" {
		if !validSHA256EvidenceDigest(evidence.SourceDigest) {
			blockers = append(blockers, "source_artifact_digest")
		}
		if !evidence.SHA256SUMS {
			blockers = append(blockers, "sha256sums")
		}
		if !evidence.ReleaseManifest {
			blockers = append(blockers, "release_manifest")
		}
	}
	return blockers
}
