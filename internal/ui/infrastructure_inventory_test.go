package ui

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
)

func TestBuildInfrastructureInventoryStaysUnavailableUntilBothSourcesAreLoaded(t *testing.T) {
	view, err := BuildInfrastructureInventory(InfrastructureInventoryInput{
		SitesLoaded: true,
		Now:         time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != InventoryViewUnavailable {
		t.Fatalf("state=%q", view.State)
	}
	if view.GeneratedAt != nil || view.SiteCount != 0 || view.NodeCount != 0 || len(view.Sites) != 0 {
		t.Fatalf("unloaded source must not look like confirmed empty inventory: %#v", view)
	}
}

func TestBuildInfrastructureInventorySortsAndPropagatesWorstFreshness(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 30, 0, 0, time.UTC)
	view, err := BuildInfrastructureInventory(InfrastructureInventoryInput{
		SitesLoaded: true,
		NodesLoaded: true,
		Now:         now,
		StaleAfter:  10 * time.Minute,
		ExpireAfter: 30 * time.Minute,
		Sites: []corecontracts.Site{
			{ID: "site-b", Name: "Beta", ScopeID: "scope-b"},
			{ID: "site-a", Name: "Alpha", ScopeID: "scope-a"},
		},
		Nodes: []NodeInventoryRecord{
			inventoryNodeRecord("node-b", "node-b.example.test", "site-b", now.Add(-5*time.Minute), agent.LinkDown),
			inventoryNodeRecord("node-a2", "zeta.example.test", "site-a", now.Add(-12*time.Minute), agent.LinkUnknown),
			inventoryNodeRecord("node-a1", "alpha.example.test", "site-a", now.Add(-2*time.Minute), agent.LinkUp),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != InventoryViewStale {
		t.Fatalf("state=%q", view.State)
	}
	if view.SiteCount != 2 || view.NodeCount != 3 {
		t.Fatalf("counts sites=%d nodes=%d", view.SiteCount, view.NodeCount)
	}
	if got := view.Sites[0].ID; got != "site-a" {
		t.Fatalf("first site=%q", got)
	}
	if got := view.Sites[0].Nodes[0].ID; got != "node-a1" {
		t.Fatalf("first node=%q", got)
	}
	if view.Sites[0].Nodes[1].Freshness != InventoryViewStale {
		t.Fatalf("stale node freshness=%q", view.Sites[0].Nodes[1].Freshness)
	}
	for _, site := range view.Sites {
		for _, node := range site.Nodes {
			if node.DesiredState != EvidenceUnavailable || node.ActualState != EvidenceUnavailable || node.VersionSkew != EvidenceUnavailable {
				t.Fatalf("unwired evidence must remain unavailable: %#v", node)
			}
		}
	}
}

func TestBuildInfrastructureInventoryExpiredEvidenceDominates(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 30, 0, 0, time.UTC)
	view, err := BuildInfrastructureInventory(InfrastructureInventoryInput{
		SitesLoaded: true,
		NodesLoaded: true,
		Now:         now,
		StaleAfter:  5 * time.Minute,
		ExpireAfter: 15 * time.Minute,
		Sites:       []corecontracts.Site{{ID: "site-a", Name: "Alpha", ScopeID: "scope-a"}},
		Nodes: []NodeInventoryRecord{
			inventoryNodeRecord("node-current", "current.example.test", "site-a", now.Add(-time.Minute), agent.LinkUp),
			inventoryNodeRecord("node-expired", "expired.example.test", "site-a", now.Add(-20*time.Minute), agent.LinkDown),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != InventoryViewExpired {
		t.Fatalf("state=%q", view.State)
	}
}

func TestBuildInfrastructureInventoryRejectsUnknownSiteWithoutPartialView(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 30, 0, 0, time.UTC)
	view, err := BuildInfrastructureInventory(InfrastructureInventoryInput{
		SitesLoaded: true,
		NodesLoaded: true,
		Now:         now,
		StaleAfter:  time.Minute,
		ExpireAfter: 2 * time.Minute,
		Sites:       []corecontracts.Site{{ID: "site-a", Name: "Alpha", ScopeID: "scope-a"}},
		Nodes:       []NodeInventoryRecord{inventoryNodeRecord("node-b", "node-b.example.test", "site-b", now, agent.LinkUp)},
	})
	if !errors.Is(err, ErrInvalidInfrastructureInventory) {
		t.Fatalf("err=%v", err)
	}
	if view.ContractVersion != "" || view.State != "" || len(view.Sites) != 0 {
		t.Fatalf("invalid source must not return a partial view: %#v", view)
	}
}

func TestNodeInventoryRecordFromEnrollmentRejectsLegacyContract(t *testing.T) {
	_, err := NodeInventoryRecordFromEnrollment(agent.EnrollmentRequest{
		NodeID:   "node-1",
		Hostname: "node-1.example.test",
	})
	if !errors.Is(err, ErrInvalidInfrastructureInventory) {
		t.Fatalf("err=%v", err)
	}
}

func inventoryNodeRecord(id, hostname, siteID string, collectedAt time.Time, link agent.LinkState) NodeInventoryRecord {
	return NodeInventoryRecord{
		NodeID:       id,
		Hostname:     hostname,
		SiteID:       siteID,
		Capabilities: []string{"Monitoring", "inventory", "inventory"},
		Roles:        []corecontracts.NodeRole{corecontracts.RoleAgent, corecontracts.RoleManagedNode, corecontracts.RoleAgent},
		CollectedAt:  collectedAt,
		Hardware: agent.HardwareInventory{
			Architecture: "amd64",
			CPU:          agent.CPUInventory{Model: "Test CPU", Sockets: 1, PhysicalCores: 4, LogicalCores: 8},
			MemoryBytes:  16 << 30,
			Storage:      []agent.StorageDevice{{ID: "disk0", Kind: agent.StorageNVMe, CapacityBytes: 512 << 30}},
		},
		NetworkInterfaces: []agent.NetworkInterface{{
			ID: "eth0", Name: "eth0", Kind: agent.InterfacePhysical, OperationalState: link,
			Zone: agent.ZoneManagement, MTU: 1500,
		}},
	}
}
