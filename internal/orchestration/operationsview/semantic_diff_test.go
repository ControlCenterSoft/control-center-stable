package operationsview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	orchestrationconfig "control-center/internal/orchestration/config"
)

func TestBuildSemanticDiffIsDeterministicValueFreeAndRevisionBound(t *testing.T) {
	base := mustSemanticRevision(t, "rev-a", 11, `{"network":{"mode":"lan"},"auth":{"password":"old-secret"},"remove_me":1}`)
	target := mustSemanticRevision(t, "rev-b", 12, `{"network":{"mode":"wan"},"auth":{"password":"new-secret"},"add_me":true}`)

	diff, err := BuildSemanticDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSemanticDiff() error = %v", err)
	}
	if diff.BaseRevisionID != base.ID() || diff.TargetRevisionID != target.ID() || diff.BaseSequence != 11 || diff.TargetSequence != 12 {
		t.Fatalf("revision binding = %#v", diff)
	}
	if diff.BaseDigest != base.Digest() || diff.TargetDigest != target.Digest() {
		t.Fatalf("digest binding = %#v", diff)
	}
	want := []SemanticDiffEntry{
		{Path: "/add_me", Kind: SemanticDiffAdded},
		{Path: "/auth/password", Kind: SemanticDiffChanged},
		{Path: "/network/mode", Kind: SemanticDiffChanged},
		{Path: "/remove_me", Kind: SemanticDiffRemoved},
	}
	if len(diff.Changes) != len(want) {
		t.Fatalf("changes = %#v", diff.Changes)
	}
	for index := range want {
		if diff.Changes[index] != want[index] {
			t.Fatalf("change[%d] = %#v want %#v", index, diff.Changes[index], want[index])
		}
	}
	encoded, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"old-secret", "new-secret", "\"lan\"", "\"wan\""} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("semantic diff leaked configuration value %q: %s", secret, encoded)
		}
	}
}

func TestBuildSemanticDiffEscapesJSONPointerPaths(t *testing.T) {
	base := mustSemanticRevision(t, "rev-a", 1, `{}`)
	target := mustSemanticRevision(t, "rev-b", 2, `{"a/b~c":true}`)
	diff, err := BuildSemanticDiff(base, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Path != "/a~1b~0c" || diff.Changes[0].Kind != SemanticDiffAdded {
		t.Fatalf("escaped diff = %#v", diff.Changes)
	}
}

func TestBuildSemanticDiffRejectsNonForwardRevisionPair(t *testing.T) {
	base := mustSemanticRevision(t, "rev-a", 5, `{"a":1}`)
	target := mustSemanticRevision(t, "rev-b", 5, `{"a":2}`)
	if _, err := BuildSemanticDiff(base, target); err == nil {
		t.Fatal("BuildSemanticDiff() accepted a non-forward revision pair")
	}
}

func TestBuildSemanticDiffFailsClosedWhenReviewSurfaceWouldTruncate(t *testing.T) {
	base := mustSemanticRevision(t, "rev-a", 1, `{}`)
	values := make(map[string]any, MaxSemanticDiffEntries+1)
	for index := 0; index <= MaxSemanticDiffEntries; index++ {
		values[fmt.Sprintf("key-%03d", index)] = index
	}
	content, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	target, err := orchestrationconfig.NewRevision("rev-b", 2, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSemanticDiff(base, target); err == nil {
		t.Fatal("BuildSemanticDiff() silently accepted an over-limit diff")
	}
}

func TestBuildSemanticDiffFailsClosedOnOverlongReviewPath(t *testing.T) {
	base := mustSemanticRevision(t, "rev-a", 1, `{}`)
	values := map[string]any{strings.Repeat("x", MaxSemanticDiffPathLength): true}
	content, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	target, err := orchestrationconfig.NewRevision("rev-b", 2, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSemanticDiff(base, target); err == nil {
		t.Fatal("BuildSemanticDiff() accepted a path longer than the API contract")
	}
}

func TestBuildSemanticDiffFailsClosedOnExcessiveNesting(t *testing.T) {
	baseValue := any(false)
	targetValue := any(true)
	for depth := 0; depth <= MaxSemanticDiffDepth+1; depth++ {
		baseValue = map[string]any{"nested": baseValue}
		targetValue = map[string]any{"nested": targetValue}
	}
	baseContent, err := json.Marshal(map[string]any{"root": baseValue})
	if err != nil {
		t.Fatal(err)
	}
	targetContent, err := json.Marshal(map[string]any{"root": targetValue})
	if err != nil {
		t.Fatal(err)
	}
	base, err := orchestrationconfig.NewRevision("rev-a", 1, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), baseContent)
	if err != nil {
		t.Fatal(err)
	}
	target, err := orchestrationconfig.NewRevision("rev-b", 2, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), targetContent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSemanticDiff(base, target); err == nil {
		t.Fatal("BuildSemanticDiff() accepted excessive nesting")
	}
}

func mustSemanticRevision(t *testing.T, id string, sequence uint64, content string) orchestrationconfig.Revision {
	t.Helper()
	revision, err := orchestrationconfig.NewRevision(id, sequence, time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), []byte(content))
	if err != nil {
		t.Fatalf("NewRevision() error = %v", err)
	}
	return revision
}
