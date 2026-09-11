package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"control-center/internal/buildinfo"
)

func TestRelease060IdentityAndSiteNetworkPackets(t *testing.T) {
	root := filepath.Join("..", "..")
	version, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if buildinfo.Version != strings.TrimSpace(string(version)) {
		t.Fatalf("release identity mismatch: VERSION=%q runtime=%q", strings.TrimSpace(string(version)), buildinfo.Version)
	}
	for _, path := range []string{
		"internal/site/model.go",
		"internal/sitesync/state.go",
		"internal/siteoffline/admission.go",
		"internal/siteoffline/cache.go",
		"internal/siteoffline/reconnect.go",
		"internal/networkpolicy/policy.go",
		"internal/networkpolicy/change_plan.go",
		"internal/networkpolicy/change_machine.go",
		"internal/edgegateway/authorization.go",
		"internal/networktelemetry/telemetry.go",
		"api/openapi-edge-gateway.yaml",
		"docs/RELEASE_0.6.0_RU.md",
	} {
		if info, statErr := os.Stat(filepath.Join(root, path)); statErr != nil || info.IsDir() {
			t.Fatalf("required 0.6 member %q is missing", path)
		}
	}
}

func TestRelease060NotesKeepNetworkAndProductionBoundaries(t *testing.T) {
	value, err := os.ReadFile(filepath.Join("..", "..", "docs", "RELEASE_0.6.0_RU.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(value)
	for _, required := range []string{
		"делегированные offline-операции",
		"автоматическим rollback",
		"запрет межзонной маршрутизации по умолчанию",
		"явное назначение Edge Gateway",
		"Network Telemetry",
		"plan-only",
		"не изменяют интерфейсы",
		"не является production deployment",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("release notes lost safety boundary %q", required)
		}
	}
}
