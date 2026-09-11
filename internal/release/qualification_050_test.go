package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"control-center/internal/buildinfo"
)

func TestRelease050IdentityAndRuntimePackets(t *testing.T) {
	root := filepath.Join("..", "..")
	version, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if buildinfo.Version != strings.TrimSpace(string(version)) {
		t.Fatalf("release identity mismatch: VERSION=%q runtime=%q", strings.TrimSpace(string(version)), buildinfo.Version)
	}
	for _, path := range []string{
		"internal/agent/admission.go",
		"internal/nodelifecycle/operation_plan.go",
		"internal/upgrade/plan.go",
		"internal/placement/plan.go",
		"internal/capacity/planner.go",
		"internal/loadtest/harness.go",
		"docs/RELEASE_0.5.0_RU.md",
	} {
		if info, statErr := os.Stat(filepath.Join(root, path)); statErr != nil || info.IsDir() {
			t.Fatalf("required 0.5 member %q is missing", path)
		}
	}
}

func TestRelease050NotesKeepExecutionAndProductionBoundaries(t *testing.T) {
	value, err := os.ReadFile(filepath.Join("..", "..", "docs", "RELEASE_0.5.0_RU.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(value)
	for _, required := range []string{"approved Change", "durable Job", "audit", "не изменяют hosts", "не является production deployment"} {
		if !strings.Contains(text, required) {
			t.Fatalf("release notes lost safety boundary %q", required)
		}
	}
}
