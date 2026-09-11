package httpapi

import (
	"strings"
	"testing"
	"time"

	productui "control-center/internal/ui"
)

func TestRenderInfrastructureInventoryDoesNotTurnUnavailableIntoZeroCounts(t *testing.T) {
	var output strings.Builder
	err := renderInfrastructureInventory(&output, "0.30.0-dev", "Администратор", "admin", productui.InfrastructureInventory{
		ContractVersion: productui.InfrastructureInventoryContractVersion,
		State:           productui.InventoryViewUnavailable,
		Sites:           []productui.SiteInventory{},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	if !strings.Contains(html, "Инвентарь недоступен") {
		t.Fatal("unavailable evidence warning is missing")
	}
	if strings.Contains(html, "Сайтов: 0") || strings.Contains(html, "Узлов: 0") {
		t.Fatal("unavailable source was rendered as a confirmed zero count")
	}
}

func TestRenderInfrastructureInventoryShowsExplicitNodeEvidenceBoundaries(t *testing.T) {
	observedAt := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	generatedAt := observedAt.Add(time.Minute)
	var output strings.Builder
	err := renderInfrastructureInventory(&output, "0.30.0-dev", "Администратор", "admin", productui.InfrastructureInventory{
		ContractVersion: productui.InfrastructureInventoryContractVersion,
		State:           productui.InventoryViewCurrent,
		GeneratedAt:     &generatedAt,
		SiteCount:       1,
		NodeCount:       1,
		Sites: []productui.SiteInventory{{
			ID: "site-a", Name: "Основная площадка", ScopeID: "scope-a", NodeCount: 1,
			Nodes: []productui.NodeInventory{{
				ID: "node-a", Hostname: "node-a.example.test", SiteID: "site-a",
				CollectedAt: observedAt, Freshness: productui.InventoryViewCurrent,
				DesiredState: productui.EvidenceUnavailable,
				ActualState:  productui.EvidenceUnavailable,
				VersionSkew:  productui.EvidenceUnavailable,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, fragment := range []string{
		"Основная площадка", "node-a.example.test", "Desired State: unavailable", "Actual State: unavailable", "Version skew: unavailable",
		"href=\"#main-content\"", "aria-label=\"Основная навигация\"", "aria-current=\"page\"",
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("missing %q", fragment)
		}
	}
	if !strings.Contains(html, "@media(max-width:780px)") || !strings.Contains(html, ".nodes{grid-template-columns:1fr}") {
		t.Fatal("mobile one-node-per-row layout contract is missing")
	}
}

func TestRenderInfrastructureInventoryKeepsStaleAndExpiredEvidenceVisible(t *testing.T) {
	observedAt := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	generatedAt := observedAt.Add(4 * time.Hour)
	var output strings.Builder
	err := renderInfrastructureInventory(&output, "0.30.0-dev", "Администратор", "admin", productui.InfrastructureInventory{
		ContractVersion: productui.InfrastructureInventoryContractVersion,
		State:           productui.InventoryViewStale,
		GeneratedAt:     &generatedAt,
		SiteCount:       1,
		NodeCount:       1,
		Sites: []productui.SiteInventory{{
			ID: "site-a", Name: "Основная площадка", ScopeID: "scope-a", NodeCount: 1,
			Nodes: []productui.NodeInventory{{
				ID: "node-a", Hostname: "node-a.example.test", SiteID: "site-a",
				CollectedAt: observedAt, Freshness: productui.InventoryViewExpired,
				DesiredState: productui.EvidenceUnavailable,
				ActualState:  productui.EvidenceUnavailable,
				VersionSkew:  productui.EvidenceUnavailable,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, fragment := range []string{"Актуальность: устарело", "Состояние evidence: устарело", "просрочено"} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("stale/expired evidence missing %q", fragment)
		}
	}
	if strings.Contains(html, "Инвентарь недоступен") {
		t.Fatal("stale evidence was incorrectly collapsed into unavailable")
	}
}
