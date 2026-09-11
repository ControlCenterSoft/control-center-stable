package ui

import "testing"

func TestNavigationFiltersAndOrdersCapabilities(t *testing.T) {
	items := Navigation(map[string]bool{"market": true, "inventory": true})
	want := []string{"overview", "inventory", "market"}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d", len(items), len(want))
	}
	for i, id := range want {
		if items[i].ID != id {
			t.Fatalf("item %d = %q, want %q", i, items[i].ID, id)
		}
	}
}

func TestNavigationAlwaysIncludesOverview(t *testing.T) {
	items := Navigation(nil)
	if len(items) != 1 || items[0].ID != "overview" {
		t.Fatalf("unexpected navigation: %#v", items)
	}
}
