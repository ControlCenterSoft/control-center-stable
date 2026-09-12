package release

import "strings"

// ReleaseEvidenceBinding ties one evidence item to the exact version and
// immutable source revision being considered for promotion. Floating refs such
// as branch names or "latest" are never accepted as release identity.
type ReleaseEvidenceBinding struct {
	Version  string
	Revision string
}

func (binding ReleaseEvidenceBinding) matches(version, revision string) bool {
	version = strings.TrimSpace(version)
	revision = strings.TrimSpace(revision)
	return version != "" && validReleaseRevision(revision) &&
		strings.TrimSpace(binding.Version) == version &&
		strings.TrimSpace(binding.Revision) == revision
}
