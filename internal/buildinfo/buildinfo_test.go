package buildinfo

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultVersionMatchesReleaseFile(t *testing.T) {
	content, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if releaseVersion := strings.TrimSpace(string(content)); Version != releaseVersion {
		t.Fatalf("buildinfo version=%q, VERSION=%q", Version, releaseVersion)
	}
}
