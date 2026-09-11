package inventory

import (
	"testing"
	"time"
)

func TestMemoryRegistryIngestAndQuery(t *testing.T) {
	registry := NewMemoryRegistry()
	t0 := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	_, err := registry.Ingest([]DeviceObservation{
		{DeviceID: "dev-1", Source: "agent", Hostname: "old", SeenAt: t0},
		{DeviceID: "dev-1", Source: "scan", Hostname: "new", SeenAt: t0.Add(time.Minute)},
		{DeviceID: "dev-2", Source: "agent", Hostname: "two", SeenAt: t0},
	})
	if err != nil {
		t.Fatal(err)
	}
	item, ok := registry.Get("dev-1")
	if !ok || item.Latest.Hostname != "new" || len(item.Sources) != 2 {
		t.Fatalf("unexpected dev-1: %#v ok=%v", item, ok)
	}
	items := registry.List()
	if len(items) != 2 || items[0].DeviceID != "dev-1" || items[1].DeviceID != "dev-2" {
		t.Fatalf("unexpected list: %#v", items)
	}
	items[0].Sources[0] = "mutated"
	again, _ := registry.Get("dev-1")
	if again.Sources[0] == "mutated" {
		t.Fatal("registry leaked mutable slice")
	}
}

func TestMemoryRegistryKeepsNewestAcrossBatches(t *testing.T) {
	registry := NewMemoryRegistry()
	t0 := time.Now().UTC()
	if _, err := registry.Ingest([]DeviceObservation{{DeviceID: "dev-1", Source: "scan", Hostname: "new", SeenAt: t0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Ingest([]DeviceObservation{{DeviceID: "dev-1", Source: "agent", Hostname: "old", SeenAt: t0.Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	item, _ := registry.Get("dev-1")
	if item.Latest.Hostname != "new" || len(item.Sources) != 2 {
		t.Fatalf("unexpected merged state: %#v", item)
	}
}
