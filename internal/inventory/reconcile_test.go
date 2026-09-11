package inventory

import (
	"testing"
	"time"
)

func TestReconcileObservationsChoosesLatest(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	devices, err := ReconcileObservations([]DeviceObservation{
		{DeviceID: "device-1", Source: "agent", Hostname: "old", SeenAt: now.Add(-time.Minute)},
		{DeviceID: "device-1", Source: "network", Hostname: "new", SeenAt: now},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(devices), 1; got != want {
		t.Fatalf("device count=%d want=%d", got, want)
	}
	if devices[0].Latest.Hostname != "new" {
		t.Fatalf("unexpected latest observation: %#v", devices[0].Latest)
	}
	if got, want := len(devices[0].Sources), 2; got != want {
		t.Fatalf("source count=%d want=%d", got, want)
	}
}

func TestReconcileObservationsSortsDevices(t *testing.T) {
	now := time.Now().UTC()
	devices, err := ReconcileObservations([]DeviceObservation{
		{DeviceID: "b", Source: "agent", SeenAt: now},
		{DeviceID: "a", Source: "agent", SeenAt: now},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devices[0].DeviceID != "a" || devices[1].DeviceID != "b" {
		t.Fatalf("unexpected order: %#v", devices)
	}
}

func TestReconcileObservationsRejectsMissingSource(t *testing.T) {
	_, err := ReconcileObservations([]DeviceObservation{{DeviceID: "device-1", SeenAt: time.Now().UTC()}})
	if err == nil {
		t.Fatal("expected error")
	}
}
