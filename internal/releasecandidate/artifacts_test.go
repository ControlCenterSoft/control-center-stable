package releasecandidate

import (
	"strings"
	"testing"
)

func completeArtifactManifest() ArtifactManifest {
	required := RequiredArtifactNames()
	artifacts := make([]ArtifactEvidence, 0, len(required))
	for _, name := range required {
		artifacts = append(artifacts, ArtifactEvidence{
			Name:   name,
			Digest: "sha256:" + strings.Repeat("e", 64),
		})
	}
	return ArtifactManifest{
		Schema:           ArtifactManifestSchemaV1,
		CandidateVersion: CandidateVersion,
		CandidateSHA:     strings.Repeat("f", 40),
		Artifacts:        artifacts,
	}
}

func TestValidateArtifactManifestAcceptsExactRequiredSet(t *testing.T) {
	if err := ValidateArtifactManifest(completeArtifactManifest()); err != nil {
		t.Fatalf("ValidateArtifactManifest() error = %v", err)
	}
}

func TestRequiredArtifactNamesReturnsDefensiveCopy(t *testing.T) {
	first := RequiredArtifactNames()
	first[0] = "weakened"
	second := RequiredArtifactNames()
	if second[0] != "control-center-0.31.0-linux-amd64.tar.gz" {
		t.Fatalf("RequiredArtifactNames() policy mutated through caller: %v", second)
	}
}

func TestValidateArtifactManifestRejectsMissingArtifact(t *testing.T) {
	manifest := completeArtifactManifest()
	manifest.Artifacts = manifest.Artifacts[:len(manifest.Artifacts)-1]

	if err := ValidateArtifactManifest(manifest); err == nil {
		t.Fatal("ValidateArtifactManifest() error = nil, want missing artifact error")
	}
}

func TestValidateArtifactManifestRejectsUnexpectedArtifact(t *testing.T) {
	manifest := completeArtifactManifest()
	manifest.Artifacts[0].Name = "control-center-0.31.0-unreviewed-extra.bin"

	if err := ValidateArtifactManifest(manifest); err == nil {
		t.Fatal("ValidateArtifactManifest() error = nil, want unexpected artifact error")
	}
}

func TestValidateArtifactManifestRejectsDuplicateArtifact(t *testing.T) {
	manifest := completeArtifactManifest()
	manifest.Artifacts = append(manifest.Artifacts, manifest.Artifacts[0])

	if err := ValidateArtifactManifest(manifest); err == nil {
		t.Fatal("ValidateArtifactManifest() error = nil, want duplicate artifact error")
	}
}

func TestValidateArtifactManifestRejectsInvalidDigest(t *testing.T) {
	manifest := completeArtifactManifest()
	manifest.Artifacts[0].Digest = "sha256:invalid"

	if err := ValidateArtifactManifest(manifest); err == nil {
		t.Fatal("ValidateArtifactManifest() error = nil, want digest error")
	}
}

func TestValidateArtifactManifestRejectsDifferentVersion(t *testing.T) {
	manifest := completeArtifactManifest()
	manifest.CandidateVersion = "0.32.0"

	if err := ValidateArtifactManifest(manifest); err == nil {
		t.Fatal("ValidateArtifactManifest() error = nil, want version error")
	}
}
