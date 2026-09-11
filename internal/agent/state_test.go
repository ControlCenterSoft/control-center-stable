package agent

import (
	"errors"
	"testing"
	"time"
)

func TestMemoryRegistryEnrollmentHeartbeatAndCopyIsolation(t *testing.T) {
	registry := NewMemoryRegistry()
	state, err := registry.Enroll(EnrollmentRequest{NodeID: "node-1", Hostname: "host1", Capabilities: []string{"PXE", "pxe", "inventory"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Enrollment.Capabilities) != 2 {
		t.Fatalf("unexpected enrollment: %#v", state)
	}
	t0 := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	state, err = registry.Heartbeat("node-1", t0)
	if err != nil || !state.LastHeartbeat.Equal(t0) {
		t.Fatalf("unexpected heartbeat: %#v err=%v", state, err)
	}
	if _, err := registry.Heartbeat("node-1", t0.Add(-time.Second)); !errors.Is(err, ErrInvalidHeartbeat) {
		t.Fatalf("expected monotonic heartbeat rejection, got %v", err)
	}
	items := registry.List()
	items[0].Enrollment.Capabilities[0] = "mutated"
	again, _ := registry.Get("node-1")
	if again.Enrollment.Capabilities[0] == "mutated" {
		t.Fatal("registry leaked mutable capability slice")
	}
}

func TestMemoryRegistryRejectsUnknownHeartbeat(t *testing.T) {
	registry := NewMemoryRegistry()
	if _, err := registry.Heartbeat("missing", time.Now().UTC()); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("expected unknown node, got %v", err)
	}
}

func TestMemoryRegistryRejectsV2UntilRichPersistenceIsAvailable(t *testing.T) {
	registry := NewMemoryRegistry()
	request := validV2Enrollment()
	_, err := registry.Enroll(request)
	if !errors.Is(err, ErrEnrollmentPersistenceUnsupported) {
		t.Fatalf("error = %v, want ErrEnrollmentPersistenceUnsupported", err)
	}
	if items := registry.List(); len(items) != 0 {
		t.Fatalf("v2 enrollment was partially persisted: %#v", items)
	}
}
