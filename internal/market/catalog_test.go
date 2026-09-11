package market

import (
	"errors"
	"testing"
)

func TestCatalogRegisterFindAndList(t *testing.T) {
	catalog := NewCatalog()
	for _, module := range []Module{
		{ID: "pxe", Version: "1.0.0", DisplayName: "PXE"},
		{ID: "inventory", Version: "1.0.0", DisplayName: "Inventory"},
	} {
		if err := catalog.Register(module); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}
	if module, ok := catalog.Find("pxe", "1.0.0"); !ok || module.DisplayName != "PXE" {
		t.Fatalf("Find() = %#v, %v", module, ok)
	}
	modules := catalog.List()
	if len(modules) != 2 || modules[0].ID != "inventory" || modules[1].ID != "pxe" {
		t.Fatalf("List() = %#v", modules)
	}
}

func TestCatalogRejectsDuplicates(t *testing.T) {
	catalog := NewCatalog()
	module := Module{ID: "pxe", Version: "1.0.0", DisplayName: "PXE"}
	if err := catalog.Register(module); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Register(module); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Register() error = %v, want ErrDuplicate", err)
	}
}

func TestCatalogRejectsInvalidModule(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(Module{ID: "pxe"}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("Register() error = %v, want ErrInvalidModule", err)
	}
}
