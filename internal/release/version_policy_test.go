package release

import "testing"

func TestForwardRelease(t *testing.T) {
	current := ReleaseVersion{Major: 0, Minor: 3, Patch: 0}
	if !IsForwardRelease(current, ReleaseVersion{Major: 0, Minor: 3, Patch: 1}) {
		t.Fatal("expected patch upgrade")
	}
	if IsForwardRelease(current, ReleaseVersion{Major: 0, Minor: 2, Patch: 9}) {
		t.Fatal("expected rollback to be rejected")
	}
	if err := ValidateReleaseVersion(ReleaseVersion{Major: 0, Minor: -1, Patch: 0}); err == nil {
		t.Fatal("expected invalid version error")
	}
}
