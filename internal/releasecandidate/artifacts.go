package releasecandidate

import "fmt"

var requiredArtifactNames = [...]string{
	"control-center-0.31.0-linux-amd64.tar.gz",
	"control-center-0.31.0-linux-amd64.tar.gz.sha256",
	"control-center-0.31.0-source.tar.gz",
	"control-center-0.31.0.sbom.cdx.json",
	"THIRD_PARTY_NOTICES.md",
	"control-center-0.31.0.provenance.json",
	"control-center-0.31.0.qualification.json",
	"control-center-0.31.0.release-manifest.json",
	"SHA256SUMS",
}

// RequiredArtifactNames returns a defensive copy so callers cannot weaken the
// expected candidate bundle by mutating package-level state.
func RequiredArtifactNames() []string {
	return append([]string(nil), requiredArtifactNames[:]...)
}

type ArtifactEvidence struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type ArtifactManifest struct {
	Schema           string             `json:"schema"`
	CandidateVersion string             `json:"candidate_version"`
	CandidateSHA     string             `json:"candidate_sha"`
	Artifacts        []ArtifactEvidence `json:"artifacts"`
}

const ArtifactManifestSchemaV1 = "control-center.release-candidate-artifacts.v1"

// ValidateArtifactManifest verifies only the bounded identity and expected file
// set for a future 0.31 candidate bundle. It does not create, download, sign,
// publish or qualify any artifact.
func ValidateArtifactManifest(manifest ArtifactManifest) error {
	if manifest.Schema != ArtifactManifestSchemaV1 {
		return fmt.Errorf("unsupported artifact manifest schema %q", manifest.Schema)
	}
	if manifest.CandidateVersion != CandidateVersion {
		return fmt.Errorf("unexpected candidate version %q", manifest.CandidateVersion)
	}
	if !shaRE.MatchString(manifest.CandidateSHA) {
		return fmt.Errorf("invalid candidate sha")
	}

	required := make(map[string]struct{}, len(requiredArtifactNames))
	for _, name := range requiredArtifactNames {
		required[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if _, ok := required[artifact.Name]; !ok {
			return fmt.Errorf("unexpected candidate artifact %q", artifact.Name)
		}
		if _, duplicate := seen[artifact.Name]; duplicate {
			return fmt.Errorf("duplicate candidate artifact %q", artifact.Name)
		}
		if !digestRE.MatchString(artifact.Digest) {
			return fmt.Errorf("candidate artifact %q has invalid digest", artifact.Name)
		}
		seen[artifact.Name] = struct{}{}
	}
	for _, name := range requiredArtifactNames {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("required candidate artifact %q is missing", name)
		}
	}
	return nil
}
