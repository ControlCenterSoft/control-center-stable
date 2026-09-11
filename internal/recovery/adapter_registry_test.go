package recovery

import (
	"errors"
	"reflect"
	"testing"
)

func restoreDrillAdapter() AdapterDescriptor {
	return AdapterDescriptor{
		AdapterID:   "postgres.pgbackrest",
		Version:     "2.54.2",
		ProviderIDs: []string{"pgbackrest-secondary", "pgbackrest"},
		Capabilities: []ProviderCapability{
			CapabilityRestoreDrill,
			CapabilityBackup,
			CapabilityRestore,
		},
	}
}

func registeredRestoreDrillRegistry(t *testing.T) *AdapterRegistry {
	t.Helper()
	registry := NewAdapterRegistry()
	if err := registry.Register(restoreDrillAdapter()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return registry
}

func TestAdapterRegistryCanonicalDetachedResolution(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	provider := restoreProvider(CapabilityRestoreDrill, CapabilityRestore)
	provider.OperationID = ""

	descriptor, err := registry.Resolve(provider, CapabilityRestoreDrill, CapabilityRestore)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(descriptor.ProviderIDs, []string{"pgbackrest", "pgbackrest-secondary"}) {
		t.Fatalf("provider IDs are not canonical: %#v", descriptor.ProviderIDs)
	}
	if !reflect.DeepEqual(descriptor.Capabilities, []ProviderCapability{CapabilityBackup, CapabilityRestore, CapabilityRestoreDrill}) {
		t.Fatalf("capabilities are not canonical: %#v", descriptor.Capabilities)
	}

	descriptor.ProviderIDs[0] = "mutated"
	descriptor.Capabilities[0] = CapabilityFencing
	again, err := registry.Resolve(provider, CapabilityRestore, CapabilityRestoreDrill)
	if err != nil {
		t.Fatal(err)
	}
	if again.ProviderIDs[0] != "pgbackrest" || again.Capabilities[0] != CapabilityBackup {
		t.Fatalf("resolved descriptor aliases registry state: %#v", again)
	}
}

func TestAdapterRegistrySupportsExactParallelVersions(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	other := restoreDrillAdapter()
	other.Version = "2.55.0"
	if err := registry.Register(other); err != nil {
		t.Fatalf("second version rejected: %v", err)
	}
	listed := registry.List()
	if len(listed) != 2 || listed[0].Version != "2.54.2" || listed[1].Version != "2.55.0" {
		t.Fatalf("versioned registrations = %#v", listed)
	}
	if err := registry.Register(other); !errors.Is(err, ErrAdapterAlreadyRegistered) {
		t.Fatalf("duplicate error = %v, want ErrAdapterAlreadyRegistered", err)
	}
}

func TestAdapterRegistryFailsClosed(t *testing.T) {
	registry := registeredRestoreDrillRegistry(t)
	base := restoreProvider(CapabilityRestore, CapabilityRestoreDrill)
	base.OperationID = ""

	tests := []struct {
		name   string
		mutate func(*ProviderMetadata)
		want   error
	}{
		{name: "unknown adapter", mutate: func(provider *ProviderMetadata) { provider.AdapterID = "postgres.unknown" }, want: ErrAdapterNotRegistered},
		{name: "unknown version", mutate: func(provider *ProviderMetadata) { provider.Version = "2.55.0" }, want: ErrAdapterNotRegistered},
		{name: "provider not allowlisted", mutate: func(provider *ProviderMetadata) { provider.ProviderID = "unregistered" }, want: ErrAdapterNotRegistered},
		{name: "overdeclared capability", mutate: func(provider *ProviderMetadata) {
			provider.Capabilities = append(provider.Capabilities, CapabilityPITR)
		}, want: ErrAdapterCapabilityMismatch},
		{name: "missing required capability", mutate: func(provider *ProviderMetadata) { provider.Capabilities = []ProviderCapability{CapabilityRestore} }, want: ErrAdapterCapabilityMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := base
			provider.Capabilities = append([]ProviderCapability(nil), base.Capabilities...)
			test.mutate(&provider)
			if _, err := registry.Resolve(provider, CapabilityRestore, CapabilityRestoreDrill); !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
		})
	}

	var unavailable *AdapterRegistry
	if _, err := unavailable.Resolve(base, CapabilityRestore); !errors.Is(err, ErrAdapterRegistryUnavailable) {
		t.Fatalf("nil registry Resolve() error = %v", err)
	}
	if err := unavailable.Register(restoreDrillAdapter()); !errors.Is(err, ErrAdapterRegistryUnavailable) {
		t.Fatalf("nil registry Register() error = %v", err)
	}
}

func TestAdapterRegistryRejectsInvalidDescriptors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdapterDescriptor)
	}{
		{name: "adapter id", mutate: func(value *AdapterDescriptor) { value.AdapterID = "shell command" }},
		{name: "version", mutate: func(value *AdapterDescriptor) { value.Version = "latest" }},
		{name: "providers absent", mutate: func(value *AdapterDescriptor) { value.ProviderIDs = nil }},
		{name: "provider duplicate", mutate: func(value *AdapterDescriptor) { value.ProviderIDs = []string{"pgbackrest", "pgbackrest"} }},
		{name: "capabilities absent", mutate: func(value *AdapterDescriptor) { value.Capabilities = nil }},
		{name: "capability duplicate", mutate: func(value *AdapterDescriptor) {
			value.Capabilities = []ProviderCapability{CapabilityRestore, CapabilityRestore}
		}},
		{name: "capability unknown", mutate: func(value *AdapterDescriptor) { value.Capabilities = []ProviderCapability{"SHELL"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := restoreDrillAdapter()
			test.mutate(&descriptor)
			if err := NewAdapterRegistry().Register(descriptor); !errors.Is(err, ErrInvalidAdapterDescriptor) {
				t.Fatalf("Register() error = %v, want ErrInvalidAdapterDescriptor", err)
			}
		})
	}
}

func TestAdapterDescriptorRemainsExecutableFree(t *testing.T) {
	typeOf := reflect.TypeOf(AdapterDescriptor{})
	want := []string{"AdapterID", "Version", "ProviderIDs", "Capabilities"}
	if typeOf.NumField() != len(want) {
		t.Fatalf("AdapterDescriptor gained an unreviewed field: %v", typeOf)
	}
	for index, field := range want {
		if typeOf.Field(index).Name != field {
			t.Fatalf("field %d = %q, want %q", index, typeOf.Field(index).Name, field)
		}
	}
}
