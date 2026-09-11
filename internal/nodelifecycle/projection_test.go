package nodelifecycle

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryProjectionIsValidatedReadOnlySnapshot(t *testing.T) {
	initial := []NodeLifecycle{lifecycleInState(StateReady), lifecycleForNode("node-2", StateOffline)}
	projection, err := NewMemoryProjection(initial)
	if err != nil {
		t.Fatalf("NewMemoryProjection() error = %v", err)
	}
	initial[0].State = StateRetired

	first, err := projection.Get(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if first.State != StateReady {
		t.Fatalf("projection changed through constructor input: %#v", first)
	}
	first.State = StateRetired
	second, err := projection.Get(context.Background(), "node-1")
	if err != nil || second.State != StateReady {
		t.Fatalf("projection changed through returned value: %#v err=%v", second, err)
	}
}

func TestMemoryProjectionRejectsInvalidOrDuplicateObjects(t *testing.T) {
	invalid := lifecycleInState(StateReady)
	invalid.ResourceVersion = ""
	if _, err := NewMemoryProjection([]NodeLifecycle{invalid}); !errors.Is(err, ErrInvalidLifecycle) {
		t.Fatalf("invalid object error = %v, want ErrInvalidLifecycle", err)
	}
	duplicate := lifecycleInState(StateReady)
	if _, err := NewMemoryProjection([]NodeLifecycle{duplicate, duplicate}); err == nil {
		t.Fatal("duplicate node accepted")
	}
}

func TestMemoryProjectionMissingUnavailableAndCancelled(t *testing.T) {
	empty := NewEmptyMemoryProjection()
	for _, nodeID := range []string{"", " missing ", "missing"} {
		if _, err := empty.Get(context.Background(), nodeID); !errors.Is(err, ErrProjectionNotFound) {
			t.Fatalf("Get(%q) error = %v, want ErrProjectionNotFound", nodeID, err)
		}
	}
	var unavailable *MemoryProjection
	if _, err := unavailable.Get(context.Background(), "node-1"); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("nil projection error = %v, want ErrProjectionUnavailable", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := empty.Get(ctx, "node-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Get() error = %v", err)
	}
}

func lifecycleForNode(nodeID string, state State) NodeLifecycle {
	lifecycle := lifecycleInState(state)
	lifecycle.ObjectID = nodeID
	lifecycle.ResourceVersion = "rv:" + nodeID
	return lifecycle
}
