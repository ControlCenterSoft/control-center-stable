package release

import "fmt"

// ReleaseVersion is a minimal semantic version used by promotion policy.
type ReleaseVersion struct {
	Major int
	Minor int
	Patch int
}

// ValidateReleaseVersion rejects negative version components.
func ValidateReleaseVersion(version ReleaseVersion) error {
	if version.Major < 0 || version.Minor < 0 || version.Patch < 0 {
		return fmt.Errorf("version components must be non-negative")
	}
	return nil
}

// IsForwardRelease reports whether candidate is strictly newer than current.
func IsForwardRelease(current, candidate ReleaseVersion) bool {
	if candidate.Major != current.Major {
		return candidate.Major > current.Major
	}
	if candidate.Minor != current.Minor {
		return candidate.Minor > current.Minor
	}
	return candidate.Patch > current.Patch
}
