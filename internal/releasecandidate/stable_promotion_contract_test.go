package releasecandidate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stablePromotionRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func stablePromotionRead(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPermanentStablePromotionV1Contract(t *testing.T) {
	root := stablePromotionRoot(t)
	workflow := stablePromotionRead(t, root, ".github/workflows/promote-qualified-stable-v1.yml")
	for _, marker := range []string{
		"name: Prepare qualified Control Center stable promotion v1",
		"on:\n  create:",
		"startsWith(github.ref_name, 'promote1/')",
		"promotion_ref=\"${GITHUB_REF_NAME#promote1/}\"",
		"version=\"${promotion_ref%%-attempt-*}\"",
		"attempt=\"${promotion_ref#\"$version\"-}\"",
		"PROMOTION_ATTEMPT_REJECTED",
		"PROMOTION_NOT_CREATED_FROM_MAIN",
		"SOURCE_REPOSITORY: ControlCenterSoft/control-center-development",
		"SOURCE_RELEASE_MISSING",
		"QUALIFICATION_STABLE_BASE_DRIFT",
		"PUBLIC_CI_NOT_EXACT_PASS",
		"SOURCE_TAG_SHA_DRIFT",
		"SOURCE_RELEASE_NOT_OFFICIAL",
		"stable_owned_dirs=['internal/releasecandidate']",
		"stable-owned-control-plane",
		"STABLE_OWNED_DIR_MISSING",
		"STABLE_OWNED_DIR_SYMLINK_REJECTED",
		"shutil.copytree(path, backup)",
		"shutil.rmtree(path, ignore_errors=True)",
		"shutil.copytree(backup, path)",
		"DEVELOPMENT_RELEASE_CONTRACT_LEAKED",
		"PERMANENT_STABLE_TOOLING_MISSING",
		"go test -race -count=1 ./...",
		"STABLE_PR_HANDOFF=REQUIRED",
		"The sole Integration writer must open the Stable promotion PR",
		"Verify Control Center Stable",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("stable promotion workflow missing %q", marker)
		}
	}

	for _, forbidden := range []string{
		"gh pr create",
		"pull-requests: write",
		"git push --force",
		"--force-with-lease",
		"refs/heads/main",
	} {
		if forbidden == "refs/heads/main" {
			// Reading main identity is required; direct writes to main are not.
			if strings.Contains(workflow, "git push origin HEAD:refs/heads/main") || strings.Contains(workflow, "git push origin main") {
				t.Fatal("promotion workflow must never push directly to Stable main")
			}
			continue
		}
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("stable promotion workflow contains forbidden mutation %q", forbidden)
		}
	}
}

func TestStablePublisherRequiresVerifiedMainAndProvenance(t *testing.T) {
	root := stablePromotionRoot(t)
	workflow := stablePromotionRead(t, root, ".github/workflows/publish-stable-release.yml")
	for _, marker := range []string{
		"name: Publish verified Control Center Stable",
		"- Verify Control Center Stable",
		"github.event.workflow_run.conclusion == 'success'",
		"github.event.workflow_run.head_branch == 'main'",
		"STABLE_VERIFY_STALE_MAIN",
		"APPROVED-SOURCE.json",
		"STABLE_PUBLICATION_NOT_AUTHORIZED",
		"RELEASE_DRIFT_APPROVED_SOURCE_TAG",
		"STABLE_RELEASE_ALREADY_PUBLISHED_IMMUTABLE",
		"RELEASE_DRIFT_MAIN_MOVED",
		"scripts/build-release.sh",
		"gh release create",
		"CONTROL_CENTER_PUBLIC_STABLE=PUBLISHED",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("stable publisher missing %q", marker)
		}
	}
	if strings.Contains(workflow, "workflow_dispatch:") {
		t.Fatal("Stable publisher must not expose a manual publication bypass")
	}
}
