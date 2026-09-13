package releasecandidate

import "fmt"

func RequiredArtifactNames() []string {
	version := CandidateVersion
	return []string{
		fmt.Sprintf("control-center-%s-linux-amd64.tar.gz", version),
		fmt.Sprintf("control-center-%s-linux-amd64.tar.gz.sha256", version),
		fmt.Sprintf("control-center-%s-source.tar.gz", version),
		fmt.Sprintf("control-center-%s.sbom.cdx.json", version),
		"THIRD_PARTY_NOTICES.md",
		fmt.Sprintf("control-center-%s.provenance.json", version),
		fmt.Sprintf("control-center-%s.qualification.json", version),
		fmt.Sprintf("control-center-%s.release-manifest.json", version),
		"SHA256SUMS",
	}
}

type ArtifactEvidence struct {
	Name string `json:"name"`
	Digest string `json:"digest"`
}

type ArtifactManifest struct {
	Schema string `json:"schema"`
	CandidateVersion string `json:"candidate_version"`
	CandidateSHA string `json:"candidate_sha"`
	Artifacts []ArtifactEvidence `json:"artifacts"`
}

const ArtifactManifestSchemaV1 = "control-center.release-candidate-artifacts.v1"

func ValidateArtifactManifest(manifest ArtifactManifest) error {
	if manifest.Schema != ArtifactManifestSchemaV1 { return fmt.Errorf("unsupported artifact manifest schema %q", manifest.Schema) }
	if manifest.CandidateVersion != CandidateVersion { return fmt.Errorf("unexpected candidate version %q", manifest.CandidateVersion) }
	if !shaRE.MatchString(manifest.CandidateSHA) { return fmt.Errorf("invalid candidate sha") }
	requiredNames := RequiredArtifactNames()
	required := make(map[string]struct{}, len(requiredNames))
	for _, name := range requiredNames { required[name] = struct{}{} }
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if _, ok := required[artifact.Name]; !ok { return fmt.Errorf("unexpected candidate artifact %q", artifact.Name) }
		if _, duplicate := seen[artifact.Name]; duplicate { return fmt.Errorf("duplicate candidate artifact %q", artifact.Name) }
		if !digestRE.MatchString(artifact.Digest) { return fmt.Errorf("candidate artifact %q has invalid digest", artifact.Name) }
		seen[artifact.Name] = struct{}{}
	}
	for _, name := range requiredNames { if _, ok := seen[name]; !ok { return fmt.Errorf("required candidate artifact %q is missing", name) } }
	return nil
}
