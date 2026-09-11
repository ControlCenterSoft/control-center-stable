package ui

import "testing"

func TestBuildOverviewStatus(t *testing.T) {
	ready := BuildOverview(OverviewInput{DomainProvider: "samba-ad-dc", DomainReady: true, InventoryCount: 12, AgentCount: 3, AgentsOnline: 3})
	if ready.Status != "ready" || len(ready.Cards) != 3 {
		t.Fatalf("unexpected ready overview: %#v", ready)
	}
	attention := BuildOverview(OverviewInput{DomainProvider: "freeipa", DomainReady: true, AgentCount: 2, AgentsOnline: 1, AgentsDelayed: 1})
	if attention.Status != "attention" {
		t.Fatalf("unexpected attention overview: %#v", attention)
	}
	degraded := BuildOverview(OverviewInput{DomainProvider: "samba-ad-dc", DomainReady: false, AgentCount: 2, AgentsOffline: 1})
	if degraded.Status != "degraded" {
		t.Fatalf("unexpected degraded overview: %#v", degraded)
	}
}
