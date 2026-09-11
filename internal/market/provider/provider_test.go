package provider

import (
	"errors"
	"testing"
)

func TestSelectUsesPriorityThenStableID(t *testing.T) {
	providers := []Provider{
		{ID: "zeta", Platforms: []string{"linux"}, Capabilities: []string{"software"}, Priority: 20},
		{ID: "alpha", Platforms: []string{"linux"}, Capabilities: []string{"software"}, Priority: 20},
		{ID: "fallback", Platforms: []string{"linux"}, Capabilities: []string{"software"}, Priority: 10},
	}
	selected, err := Select(providers, "linux", "software")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "alpha" {
		t.Fatalf("selected %q, want alpha", selected.ID)
	}
}

func TestSelectRejectsMissingCompatibility(t *testing.T) {
	_, err := Select(nil, "windows", "pxe")
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("got %v, want ErrNoProvider", err)
	}
}
