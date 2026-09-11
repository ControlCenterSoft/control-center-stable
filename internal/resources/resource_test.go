package resources

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryRegistryListAndGet(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	registry, err := NewMemoryRegistry([]Resource{{ID: "node-2", OrganizationID: "org-a", Kind: "node", Name: "Node 2", Status: "ready", Revision: 1, CreatedAt: now, UpdatedAt: now}, {ID: "site-1", OrganizationID: "org-a", Kind: "site", Name: "Site 1", Status: "ready", Revision: 2, CreatedAt: now, UpdatedAt: now}, {ID: "node-1", OrganizationID: "org-b", Kind: "node", Name: "Node 1", Status: "unknown", Revision: 1, CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		t.Fatalf("NewMemoryRegistry() error = %v", err)
	}
	items, err := registry.List(context.Background(), Filter{OrganizationID: "org-a", Kind: "node"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "node-2" {
		t.Fatalf("List() = %#v, want node-2", items)
	}
	item, err := registry.Get(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if item.Revision != 2 {
		t.Fatalf("Get().Revision = %d, want 2", item.Revision)
	}
}
func TestMemoryRegistryNotFound(t *testing.T) {
	registry, err := NewMemoryRegistry(nil)
	if err != nil {
		t.Fatalf("NewMemoryRegistry() error = %v", err)
	}
	_, err = registry.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}
func TestMemoryRegistryRejectsInvalidResource(t *testing.T) {
	_, err := NewMemoryRegistry([]Resource{{ID: "bad", Revision: 1}})
	if err == nil {
		t.Fatal("NewMemoryRegistry() error = nil, want validation error")
	}
}
