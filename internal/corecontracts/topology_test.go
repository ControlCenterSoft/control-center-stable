package corecontracts

import (
	"errors"
	"testing"
)

func TestNewTopologySupportsArbitraryScopeDepth(t *testing.T) {
	scopes, sites, zones := testTopologyInput()
	topology, err := NewTopology(scopes, sites, zones)
	if err != nil {
		t.Fatalf("NewTopology() error = %v", err)
	}
	if topology.RootID() != "global" {
		t.Fatalf("RootID() = %q, want global", topology.RootID())
	}
	if !topology.Contains("global", "site-a-resources") || !topology.Contains("region-a", "site-a") {
		t.Fatal("valid nested scope relation not found")
	}
	if topology.Contains("site-a", "region-a") || topology.Contains("missing", "site-a") {
		t.Fatal("invalid nested scope relation accepted")
	}
	if topology.Contains("missing", "missing") {
		t.Fatal("equal unknown scope IDs were treated as a valid relation")
	}
	if !topology.IsDelegated("site-a", DelegateDesiredState) {
		t.Fatal("explicit desired-state delegation not found")
	}
	if topology.IsDelegated("site-a-resources", DelegateDesiredState) {
		t.Fatal("delegation was inherited implicitly")
	}
	if _, ok := topology.Site("site-a"); !ok {
		t.Fatal("site lookup failed")
	}
	if _, ok := topology.ManagementZone("zone-a"); !ok {
		t.Fatal("management zone lookup failed")
	}

	// Constructor inputs and values returned by lookup cannot mutate the index.
	scopes[2].DelegatedAuthorities[0] = DelegateRBAC
	scope, ok := topology.Scope("site-a")
	if !ok || scope.DelegatedAuthorities[0] != DelegateDesiredState {
		t.Fatalf("topology changed through constructor input: %#v", scope)
	}
	scope.DelegatedAuthorities[0] = DelegateApplications
	scopeAgain, _ := topology.Scope("site-a")
	if scopeAgain.DelegatedAuthorities[0] != DelegateDesiredState {
		t.Fatalf("topology changed through lookup result: %#v", scopeAgain)
	}
}

func TestNewTopologyRejectsInvalidScopeGraphs(t *testing.T) {
	valid, _, _ := testTopologyInput()
	tests := []struct {
		name   string
		mutate func([]Scope) []Scope
	}{
		{name: "no global root", mutate: func(scopes []Scope) []Scope { return scopes[1:] }},
		{name: "multiple roots", mutate: func(scopes []Scope) []Scope {
			return append(scopes, Scope{ID: "global-2", Kind: ScopeGlobal, Name: "Other global"})
		}},
		{name: "missing parent", mutate: func(scopes []Scope) []Scope {
			scopes[2].ParentID = "missing"
			return scopes
		}},
		{name: "cycle", mutate: func(scopes []Scope) []Scope {
			scopes[1].ParentID = "site-a"
			return scopes
		}},
		{name: "self parent", mutate: func(scopes []Scope) []Scope {
			scopes[2].ParentID = "site-a"
			return scopes
		}},
		{name: "duplicate scope", mutate: func(scopes []Scope) []Scope { return append(scopes, scopes[1]) }},
		{name: "unknown delegation", mutate: func(scopes []Scope) []Scope {
			scopes[2].DelegatedAuthorities = []DelegatedAuthority{"shell"}
			return scopes
		}},
		{name: "duplicate delegation", mutate: func(scopes []Scope) []Scope {
			scopes[2].DelegatedAuthorities = []DelegatedAuthority{DelegateRBAC, DelegateRBAC}
			return scopes
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scopes := cloneScopes(valid)
			scopes = test.mutate(scopes)
			if _, err := NewTopology(scopes, nil, nil); !errors.Is(err, ErrInvalidTopology) {
				t.Fatalf("NewTopology() error = %v, want ErrInvalidTopology", err)
			}
		})
	}
}

func TestNewTopologyRejectsInvalidSitesAndZones(t *testing.T) {
	scopes, sites, zones := testTopologyInput()
	tests := []struct {
		name  string
		sites []Site
		zones []ManagementZone
	}{
		{name: "site uses non-site scope", sites: []Site{{ID: "bad", Name: "Bad", ScopeID: "region-a"}}},
		{name: "duplicate site scope", sites: append(sites, Site{ID: "site-b", Name: "B", ScopeID: "site-a"})},
		{name: "unknown zone scope", sites: sites, zones: []ManagementZone{{ID: "bad", Name: "Bad", ScopeID: "missing"}}},
		{name: "unknown zone site", sites: sites, zones: []ManagementZone{{ID: "bad", Name: "Bad", ScopeID: "site-a", SiteID: "missing"}}},
		{name: "zone outside site", sites: sites, zones: []ManagementZone{{ID: "bad", Name: "Bad", ScopeID: "region-a", SiteID: "site-a"}}},
		{name: "duplicate zone", sites: sites, zones: append(zones, zones[0])},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTopology(cloneScopes(scopes), test.sites, test.zones); !errors.Is(err, ErrInvalidTopology) {
				t.Fatalf("NewTopology() error = %v, want ErrInvalidTopology", err)
			}
		})
	}
}

func testTopology(t *testing.T) Topology {
	t.Helper()
	scopes, sites, zones := testTopologyInput()
	topology, err := NewTopology(scopes, sites, zones)
	if err != nil {
		t.Fatalf("NewTopology() error = %v", err)
	}
	return topology
}

func testTopologyInput() ([]Scope, []Site, []ManagementZone) {
	scopes := []Scope{
		{ID: "global", Kind: ScopeGlobal, Name: "Global"},
		{ID: "region-a", Kind: ScopeManagement, Name: "Region A", ParentID: "global", DelegatedAuthorities: []DelegatedAuthority{DelegatePolicy}},
		{ID: "site-a", Kind: ScopeSite, Name: "Site A", ParentID: "region-a", DelegatedAuthorities: []DelegatedAuthority{DelegateDesiredState, DelegateRBAC}},
		{ID: "site-a-resources", Kind: ScopeResource, Name: "Site A resources", ParentID: "site-a"},
	}
	sites := []Site{{ID: "site-a", Name: "Site A", ScopeID: "site-a"}}
	zones := []ManagementZone{{ID: "zone-a", Name: "Site A management", ScopeID: "site-a", SiteID: "site-a"}}
	return scopes, sites, zones
}

func cloneScopes(scopes []Scope) []Scope {
	result := make([]Scope, len(scopes))
	for index, scope := range scopes {
		result[index] = cloneScope(scope)
	}
	return result
}
