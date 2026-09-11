package sitesync

import (
	"errors"
	"testing"
)

func desired(generation uint64, version, hash string) Record {
	return Record{
		SiteID:          "site-eu-1",
		ResourceID:      "network-policy/default",
		Kind:            DesiredState,
		Authority:       GlobalAuthority,
		Generation:      generation,
		ResourceVersion: version,
		PayloadHash:     hash,
	}
}

func TestDesiredAndActualDirections(t *testing.T) {
	wanted := desired(1, "rv-1", "hash-1")
	direction, err := wanted.Direction()
	if err != nil {
		t.Fatalf("Direction() error = %v", err)
	}
	if direction != "global-to-site" {
		t.Fatalf("Direction() = %q, want global-to-site", direction)
	}

	actual := Record{
		SiteID:          "site-eu-1",
		ResourceID:      "node/node-1",
		Kind:            ActualState,
		Authority:       SiteAuthority,
		Generation:      1,
		ResourceVersion: "rv-a1",
		PayloadHash:     "hash-a1",
	}
	direction, err = actual.Direction()
	if err != nil {
		t.Fatalf("Direction() error = %v", err)
	}
	if direction != "site-to-global" {
		t.Fatalf("Direction() = %q, want site-to-global", direction)
	}
}

func TestValidateRejectsWrongAuthority(t *testing.T) {
	record := desired(1, "rv-1", "hash-1")
	record.Authority = SiteAuthority
	if err := record.Validate(); !errors.Is(err, ErrWrongAuthority) {
		t.Fatalf("Validate() error = %v, want ErrWrongAuthority", err)
	}
}

func TestReconcileAppliesNewerGeneration(t *testing.T) {
	current := desired(1, "rv-1", "hash-1")
	incoming := desired(2, "rv-2", "hash-2")
	got, changed, err := Reconcile(current, incoming)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !changed || got.Generation != 2 || got.ResourceVersion != "rv-2" {
		t.Fatalf("Reconcile() = %#v, changed=%v", got, changed)
	}
}

func TestReconcileIsIdempotentForExactRevision(t *testing.T) {
	current := desired(4, "rv-4", "hash-4")
	got, changed, err := Reconcile(current, current)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if changed || got != current {
		t.Fatalf("Reconcile() changed=%v got=%#v, want unchanged", changed, got)
	}
}

func TestReconcileRejectsStaleGeneration(t *testing.T) {
	_, _, err := Reconcile(desired(3, "rv-3", "hash-3"), desired(2, "rv-2", "hash-2"))
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Reconcile() error = %v, want ErrStaleGeneration", err)
	}
}

func TestReconcileSurfacesSameGenerationConflict(t *testing.T) {
	_, _, err := Reconcile(desired(7, "rv-7a", "hash-a"), desired(7, "rv-7b", "hash-b"))
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("Reconcile() error = %v, want ErrStateConflict", err)
	}
}
