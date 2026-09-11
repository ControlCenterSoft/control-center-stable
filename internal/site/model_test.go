package site

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateHierarchyAndLineage(t *testing.T) {
	sites := []Site{
		{ID: "global", Name: "Global"},
		{ID: "eu", Name: "Europe", ParentID: "global", Delegated: true},
		{ID: "ams", Name: "Amsterdam", ParentID: "eu", Delegated: true},
	}
	if err := ValidateHierarchy(sites); err != nil {
		t.Fatalf("ValidateHierarchy() error = %v", err)
	}
	got, err := Lineage("ams", sites)
	if err != nil {
		t.Fatalf("Lineage() error = %v", err)
	}
	want := []ID{"global", "eu", "ams"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lineage() = %v, want %v", got, want)
	}
}

func TestValidateHierarchyRejectsMissingParent(t *testing.T) {
	err := ValidateHierarchy([]Site{{ID: "branch", Name: "Branch", ParentID: "missing"}})
	if !errors.Is(err, ErrMissingParent) {
		t.Fatalf("ValidateHierarchy() error = %v, want ErrMissingParent", err)
	}
}

func TestValidateHierarchyRejectsCycles(t *testing.T) {
	err := ValidateHierarchy([]Site{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a"},
	})
	if !errors.Is(err, ErrHierarchyCycle) {
		t.Fatalf("ValidateHierarchy() error = %v, want ErrHierarchyCycle", err)
	}
}

func TestValidateHierarchyRejectsDuplicateIDs(t *testing.T) {
	err := ValidateHierarchy([]Site{
		{ID: "site-a", Name: "A"},
		{ID: "site-a", Name: "Duplicate"},
	})
	if !errors.Is(err, ErrDuplicateSite) {
		t.Fatalf("ValidateHierarchy() error = %v, want ErrDuplicateSite", err)
	}
}
