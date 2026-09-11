package market

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func uint64Pointer(value uint64) *uint64 { return &value }

func validProductionManifestV2() ManifestV2 {
	return ManifestV2{
		SchemaVersion: ManifestSchemaV2,
		ID:            "example-service",
		Version:       "1.2.3",
		Status:        ManifestStatusProductionReady,
		Capabilities:  []string{"example-api", "example-health"},
		Providers:     []string{"example-provider"},
		WorkloadType:  WorkloadStateless,
		Lifecycle: LifecycleSpec{Operations: []LifecycleOperationV2{
			LifecycleV2Install,
			LifecycleV2Configure,
			LifecycleV2Health,
			LifecycleV2Capacity,
			LifecycleV2ScalePlacement,
			LifecycleV2Update,
			LifecycleV2Backup,
			LifecycleV2Restore,
			LifecycleV2Remove,
		}},
		Capacity: CapacityProfile{
			Status:            CapacityEstimated,
			WorkloadUnit:      "managed-device",
			Minimum:           ResourceEnvelope{CPUMillicores: 500, MemoryMiB: 512, StorageMiB: 1024, StorageIOPS: 100, NetworkMbps: 10, DatabaseConnections: 5, ServiceQueueDepth: 100},
			SafeUnits:         1000,
			TechnicalLimit:    1500,
			Confidence:        CapacityConfidenceMedium,
			BenchmarkScenario: "example-load-test",
			Evidence: []CapacityEvidence{{
				ID: "baseline-2026", Kind: CapacityEvidenceLoadTest, Reference: "urn:cc:benchmark:example-load-test:2026-09",
				Digest: "sha256:" + strings.Repeat("a", 64),
			}},
		},
		Recovery: RecoverySpec{
			Adapter: "example-recovery", Backup: RecoverySupported, Restore: RecoverySupported, Failover: RecoveryNotApplicable,
		},
		Network: NetworkSpec{
			Enforcement: NetworkPolicyControlled,
			Requirements: []NetworkRequirement{{
				Name: "https-api", Protocol: NetworkTCP, Direction: NetworkIngress, Exposure: NetworkInternal,
				Zones: []string{"LAN", "MANAGEMENT"}, Ports: []PortRange{{From: 8443, To: 8443}},
			}},
		},
		Dependencies: DependencyMetadata{
			Modules:      []ModuleRequirement{{ID: "monitoring", VersionConstraint: ">=1.0.0 <2.0.0", Kind: DependencyRequired}},
			Capabilities: []string{"metrics"},
			Storage: []StorageRequirement{{
				Name: "state", Class: StorageFilesystem, AccessMode: StorageReadWriteOnce, MinimumMiB: 1024, Persistent: true,
			}},
			Secrets: []SecretRequirement{{Name: "api-credential", Purpose: "authenticate the provider API", Scope: SecretInstance}},
		},
		Compatibility: CompatibilitySpec{
			Platforms: []string{"linux"}, Architectures: []string{"amd64", "arm64"}, MinControllerVersion: "0.4.0", MaxControllerVersion: "1.9.0",
		},
		Activation: ActivationPolicy{Mode: ActivationExplicit, NetworkChanges: NetworkApprovalRequired},
	}
}

func TestValidateManifestV2ProductionReady(t *testing.T) {
	manifest := validProductionManifestV2()
	if err := ValidateManifestV2(manifest); err != nil {
		t.Fatalf("ValidateManifestV2() error = %v", err)
	}
}

func TestValidateManifestV2ClusteredRecovery(t *testing.T) {
	manifest := validProductionManifestV2()
	manifest.WorkloadType = WorkloadClustered
	manifest.Lifecycle.Operations = append(manifest.Lifecycle.Operations, LifecycleV2Migrate, LifecycleV2Drain, LifecycleV2Failover)
	manifest.Recovery.Failover = RecoverySupported
	manifest.Recovery.FencingRequired = true
	manifest.Recovery.RPOSeconds = uint64Pointer(300)
	manifest.Recovery.RTOSeconds = uint64Pointer(900)
	if err := ValidateManifestV2(manifest); err != nil {
		t.Fatalf("ValidateManifestV2() error = %v", err)
	}
}

func TestValidateManifestV2RejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name  string
		field string
		edit  func(*ManifestV2)
	}{
		{name: "schema", field: "schema_version", edit: func(m *ManifestV2) { m.SchemaVersion = ManifestSchemaV1 }},
		{name: "id whitespace", field: "id", edit: func(m *ManifestV2) { m.ID = " example" }},
		{name: "version", field: "version", edit: func(m *ManifestV2) { m.Version = "1.2" }},
		{name: "status", field: "status", edit: func(m *ManifestV2) { m.Status = "READY" }},
		{name: "empty capabilities", field: "capabilities", edit: func(m *ManifestV2) { m.Capabilities = nil }},
		{name: "duplicate capabilities", field: "capabilities", edit: func(m *ManifestV2) { m.Capabilities = []string{"api", "api"} }},
		{name: "invalid provider", field: "providers[0]", edit: func(m *ManifestV2) { m.Providers = []string{"Provider"} }},
		{name: "workload", field: "workload_type", edit: func(m *ManifestV2) { m.WorkloadType = "DAEMON" }},
		{name: "unspecified production workload", field: "workload_type", edit: func(m *ManifestV2) { m.WorkloadType = WorkloadUnspecified }},
		{name: "empty lifecycle", field: "lifecycle.operations", edit: func(m *ManifestV2) { m.Lifecycle.Operations = nil }},
		{name: "unknown lifecycle", field: "lifecycle.operations[9]", edit: func(m *ManifestV2) { m.Lifecycle.Operations = append(m.Lifecycle.Operations, "restart") }},
		{name: "duplicate lifecycle", field: "lifecycle.operations", edit: func(m *ManifestV2) { m.Lifecycle.Operations = append(m.Lifecycle.Operations, LifecycleV2Install) }},
		{name: "missing health", field: "lifecycle.operations", edit: func(m *ManifestV2) {
			m.Lifecycle.Operations = removeLifecycleOperation(m.Lifecycle.Operations, LifecycleV2Health)
		}},
		{name: "capacity status", field: "capacity.status", edit: func(m *ManifestV2) { m.Capacity.Status = "TRUSTED" }},
		{name: "capacity unit", field: "capacity.workload_unit", edit: func(m *ManifestV2) { m.Capacity.WorkloadUnit = "managed device" }},
		{name: "zero safe units", field: "capacity", edit: func(m *ManifestV2) { m.Capacity.SafeUnits = 0 }},
		{name: "reversed capacity limits", field: "capacity", edit: func(m *ManifestV2) { m.Capacity.SafeUnits = m.Capacity.TechnicalLimit + 1 }},
		{name: "missing capacity evidence", field: "capacity.evidence", edit: func(m *ManifestV2) { m.Capacity.Evidence = nil }},
		{name: "duplicate capacity evidence", field: "capacity.evidence", edit: func(m *ManifestV2) { m.Capacity.Evidence = append(m.Capacity.Evidence, m.Capacity.Evidence[0]) }},
		{name: "capacity digest", field: "capacity.evidence[0].digest", edit: func(m *ManifestV2) { m.Capacity.Evidence[0].Digest = "sha256:no" }},
		{name: "zero capacity envelope", field: "capacity.minimum", edit: func(m *ManifestV2) { m.Capacity.Minimum = ResourceEnvelope{} }},
		{name: "production capacity unverified", field: "capacity.status", edit: func(m *ManifestV2) {
			m.Capacity = CapacityProfile{Status: CapacityUnverified, WorkloadUnit: "instance", Confidence: CapacityConfidenceUnknown}
		}},
		{name: "recovery capability", field: "recovery.backup", edit: func(m *ManifestV2) { m.Recovery.Backup = "MAYBE" }},
		{name: "backup mismatch", field: "recovery.backup", edit: func(m *ManifestV2) { m.Recovery.Backup = RecoveryNotApplicable }},
		{name: "failover without operation", field: "recovery.failover", edit: func(m *ManifestV2) { m.Recovery.Failover = RecoverySupported }},
		{name: "fencing without failover", field: "recovery.fencing_required", edit: func(m *ManifestV2) { m.Recovery.FencingRequired = true }},
		{name: "network enforcement", field: "network.enforcement", edit: func(m *ManifestV2) { m.Network.Enforcement = "AUTO_APPLY" }},
		{name: "network duplicate names", field: "network.requirements", edit: func(m *ManifestV2) {
			m.Network.Requirements = append(m.Network.Requirements, m.Network.Requirements[0])
		}},
		{name: "network protocol", field: "network.requirements[0].protocol", edit: func(m *ManifestV2) { m.Network.Requirements[0].Protocol = "ANY" }},
		{name: "network direction", field: "network.requirements[0].direction", edit: func(m *ManifestV2) { m.Network.Requirements[0].Direction = "BOTH" }},
		{name: "network exposure", field: "network.requirements[0].exposure", edit: func(m *ManifestV2) { m.Network.Requirements[0].Exposure = "PUBLIC" }},
		{name: "network zones missing", field: "network.requirements[0].zones", edit: func(m *ManifestV2) { m.Network.Requirements[0].Zones = nil }},
		{name: "network zone malformed", field: "network.requirements[0].zones[0]", edit: func(m *ManifestV2) { m.Network.Requirements[0].Zones = []string{"lan"} }},
		{name: "network duplicate zone", field: "network.requirements[0].zones", edit: func(m *ManifestV2) { m.Network.Requirements[0].Zones = []string{"LAN", "LAN"} }},
		{name: "network ports missing", field: "network.requirements[0].ports", edit: func(m *ManifestV2) { m.Network.Requirements[0].Ports = nil }},
		{name: "network port zero", field: "network.requirements[0].ports", edit: func(m *ManifestV2) { m.Network.Requirements[0].Ports = []PortRange{{From: 0, To: 1}} }},
		{name: "network port reversed", field: "network.requirements[0].ports", edit: func(m *ManifestV2) { m.Network.Requirements[0].Ports = []PortRange{{From: 100, To: 90}} }},
		{name: "network ports overlap", field: "network.requirements[0].ports", edit: func(m *ManifestV2) {
			m.Network.Requirements[0].Ports = []PortRange{{From: 8000, To: 8100}, {From: 8050, To: 8200}}
		}},
		{name: "dependency self", field: "dependencies.modules[0].id", edit: func(m *ManifestV2) { m.Dependencies.Modules[0].ID = m.ID }},
		{name: "dependency duplicate", field: "dependencies.modules", edit: func(m *ManifestV2) {
			m.Dependencies.Modules = append(m.Dependencies.Modules, m.Dependencies.Modules[0])
		}},
		{name: "dependency constraint", field: "dependencies.modules[0].version_constraint", edit: func(m *ManifestV2) { m.Dependencies.Modules[0].VersionConstraint = "latest" }},
		{name: "dependency contradictory range", field: "dependencies.modules[0].version_constraint", edit: func(m *ManifestV2) { m.Dependencies.Modules[0].VersionConstraint = ">=2.0.0 <1.0.0" }},
		{name: "dependency ambiguous pair", field: "dependencies.modules[0].version_constraint", edit: func(m *ManifestV2) { m.Dependencies.Modules[0].VersionConstraint = "1.0.0 2.0.0" }},
		{name: "dependency kind", field: "dependencies.modules[0].kind", edit: func(m *ManifestV2) { m.Dependencies.Modules[0].Kind = "HIDDEN" }},
		{name: "storage class", field: "dependencies.storage[0].class", edit: func(m *ManifestV2) { m.Dependencies.Storage[0].Class = "MAGIC" }},
		{name: "storage size", field: "dependencies.storage[0].minimum_mib", edit: func(m *ManifestV2) { m.Dependencies.Storage[0].MinimumMiB = 0 }},
		{name: "secret purpose", field: "dependencies.secrets[0].purpose", edit: func(m *ManifestV2) { m.Dependencies.Secrets[0].Purpose = " " }},
		{name: "secret scope", field: "dependencies.secrets[0].scope", edit: func(m *ManifestV2) { m.Dependencies.Secrets[0].Scope = "GLOBAL" }},
		{name: "platform", field: "compatibility.platforms[0]", edit: func(m *ManifestV2) { m.Compatibility.Platforms = []string{"darwin"} }},
		{name: "architecture", field: "compatibility.architectures[0]", edit: func(m *ManifestV2) { m.Compatibility.Architectures = []string{"386"} }},
		{name: "architectures missing", field: "compatibility.architectures", edit: func(m *ManifestV2) { m.Compatibility.Architectures = nil }},
		{name: "compatibility range", field: "compatibility", edit: func(m *ManifestV2) {
			m.Compatibility.MinControllerVersion, m.Compatibility.MaxControllerVersion = "2.0.0", "1.0.0"
		}},
		{name: "activation", field: "activation.mode", edit: func(m *ManifestV2) { m.Activation.Mode = "AUTOMATIC" }},
		{name: "network activation", field: "activation.network_changes", edit: func(m *ManifestV2) { m.Activation.NetworkChanges = "AUTO_APPLY" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validProductionManifestV2()
			test.edit(&manifest)
			err := ValidateManifestV2(manifest)
			if err == nil {
				t.Fatal("ValidateManifestV2() accepted invalid manifest")
			}
			var validation *ManifestValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error type = %T, want *ManifestValidationError", err)
			}
			if validation.Field != test.field {
				t.Fatalf("validation field = %q, want %q (error: %v)", validation.Field, test.field, err)
			}
		})
	}
}

func TestValidateManifestV2RejectsIncompleteStatefulRecovery(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ManifestV2)
	}{
		{name: "missing adapter", edit: func(m *ManifestV2) { m.Recovery.Adapter = "" }},
		{name: "missing rpo", edit: func(m *ManifestV2) { m.Recovery.RPOSeconds = nil }},
		{name: "zero rto", edit: func(m *ManifestV2) { m.Recovery.RTOSeconds = uint64Pointer(0) }},
		{name: "failover without fencing", edit: func(m *ManifestV2) { m.Recovery.FencingRequired = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validProductionManifestV2()
			manifest.WorkloadType = WorkloadClustered
			manifest.Lifecycle.Operations = append(manifest.Lifecycle.Operations, LifecycleV2Migrate, LifecycleV2Drain, LifecycleV2Failover)
			manifest.Recovery.Failover = RecoverySupported
			manifest.Recovery.FencingRequired = true
			manifest.Recovery.RPOSeconds = uint64Pointer(300)
			manifest.Recovery.RTOSeconds = uint64Pointer(900)
			test.edit(&manifest)
			if err := ValidateManifestV2(manifest); err == nil {
				t.Fatal("ValidateManifestV2() accepted incomplete stateful recovery")
			}
		})
	}
}

func TestCapacityUnverifiedCannotCarryClaims(t *testing.T) {
	manifest, err := MigrateBuiltinManifestV1(BuiltinManifest{
		ID: "legacy", Version: "1.0.0", Capabilities: []string{"legacy"}, Platforms: []string{"linux"},
		Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capacity.SafeUnits = 1
	if err := ValidateManifestV2(manifest); err == nil {
		t.Fatal("unverified capacity claim was accepted")
	}
}

func TestDecodeManifestV2IsStrict(t *testing.T) {
	valid, err := json.Marshal(validProductionManifestV2())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManifestV2(valid)
	if err != nil {
		t.Fatalf("DecodeManifestV2() error = %v", err)
	}
	if decoded.ID != "example-service" {
		t.Fatalf("decoded manifest = %#v", decoded)
	}

	unknown := append([]byte(nil), valid[:len(valid)-1]...)
	unknown = append(unknown, []byte(`,"auto_activate":true}`)...)
	if _, err := DecodeManifestV2(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	duplicate := append([]byte(nil), valid[:len(valid)-1]...)
	duplicate = append(duplicate, []byte(`,"id":"shadow"}`)...)
	if _, err := DecodeManifestV2(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate field") {
		t.Fatalf("duplicate field error = %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(valid, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, "dependencies")
	missing, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifestV2(missing); err == nil || !strings.Contains(err.Error(), "dependencies") {
		t.Fatalf("missing required field error = %v", err)
	}
	if _, err := DecodeManifestV2(append(valid, []byte(` {}`)...)); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
	if _, err := DecodeManifestV2([]byte(`{"schema_version":`)); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
}

func TestCompatibilityComparisonHandlesLargeVersions(t *testing.T) {
	manifest := validProductionManifestV2()
	manifest.Compatibility.MinControllerVersion = "184467440737095516160.0.0"
	manifest.Compatibility.MaxControllerVersion = "184467440737095516161.0.0"
	if err := ValidateManifestV2(manifest); err != nil {
		t.Fatalf("large ordered semantic versions rejected: %v", err)
	}
	manifest.Compatibility.MinControllerVersion, manifest.Compatibility.MaxControllerVersion = manifest.Compatibility.MaxControllerVersion, manifest.Compatibility.MinControllerVersion
	if err := ValidateManifestV2(manifest); err == nil {
		t.Fatal("reversed large semantic versions accepted")
	}
}

func TestMigrateBuiltinManifestV1IsDeterministicAndConservative(t *testing.T) {
	source := BuiltinManifest{
		ID: "legacy-module", Version: "1.2.3",
		Capabilities: []string{"zeta", "alpha", "zeta"},
		Platforms:    []string{"windows", "linux", "linux"},
		Providers:    []string{"provider-b", "provider-a"},
		Lifecycle:    []LifecycleOperation{LifecycleRemove, LifecycleInstall, LifecycleUpgrade},
	}
	first, err := MigrateBuiltinManifestV1(source)
	if err != nil {
		t.Fatalf("MigrateBuiltinManifestV1() error = %v", err)
	}
	second, err := MigrateBuiltinManifestV1(source)
	if err != nil {
		t.Fatalf("MigrateBuiltinManifestV1() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("migration is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.Status != ManifestStatusTransitional || first.WorkloadType != WorkloadUnspecified {
		t.Fatalf("migration invented readiness or workload type: %#v", first)
	}
	wantLifecycle := []LifecycleOperationV2{LifecycleV2Install, LifecycleV2Update, LifecycleV2Remove}
	if !reflect.DeepEqual(first.Lifecycle.Operations, wantLifecycle) {
		t.Fatalf("lifecycle = %#v, want %#v", first.Lifecycle.Operations, wantLifecycle)
	}
	if first.Capacity.Status != CapacityUnverified || first.Capacity.SafeUnits != 0 || first.Capacity.TechnicalLimit != 0 {
		t.Fatalf("migration invented capacity claims: %#v", first.Capacity)
	}
	if first.Recovery.Backup != RecoveryUndeclared || first.Recovery.Restore != RecoveryUndeclared || first.Recovery.Failover != RecoveryUndeclared {
		t.Fatalf("migration invented recovery capabilities: %#v", first.Recovery)
	}
	if first.Network.Enforcement != NetworkPolicyControlled || len(first.Network.Requirements) != 0 {
		t.Fatalf("migration activated network requirements: %#v", first.Network)
	}
	if first.Activation.Mode != ActivationExplicit || first.Activation.NetworkChanges != NetworkApprovalRequired {
		t.Fatalf("migration permits hidden activation: %#v", first.Activation)
	}
	if len(first.Migration.Warnings) < 5 {
		t.Fatalf("migration does not disclose v1 gaps: %#v", first.Migration)
	}
	if !reflect.DeepEqual(first.Capabilities, []string{"alpha", "zeta"}) || !reflect.DeepEqual(first.Compatibility.Platforms, []string{"linux", "windows"}) {
		t.Fatalf("migration sets are not canonical: capabilities=%#v platforms=%#v", first.Capabilities, first.Compatibility.Platforms)
	}
	if err := ValidateManifestV2(first); err != nil {
		t.Fatalf("migrated manifest invalid: %v", err)
	}
}

func TestMigrateBuiltinManifestV1RejectsInvalidSource(t *testing.T) {
	_, err := MigrateBuiltinManifestV1(BuiltinManifest{ID: "legacy", Version: "bad"})
	if err == nil {
		t.Fatal("invalid v1 source was accepted")
	}
}

func TestBuiltinManifestsV2AreValidSortedAndBackwardCompatible(t *testing.T) {
	v1 := BuiltinManifests()
	v2, err := BuiltinManifestsV2()
	if err != nil {
		t.Fatalf("BuiltinManifestsV2() error = %v", err)
	}
	if len(v2) != len(v1) {
		t.Fatalf("v2 count = %d, want %d", len(v2), len(v1))
	}
	for index := range v2 {
		if err := ValidateManifestV2(v2[index]); err != nil {
			t.Fatalf("manifest %q invalid: %v", v2[index].ID, err)
		}
		if v2[index].ID != v1[index].ID || v2[index].Version != v1[index].Version {
			t.Fatalf("identity changed at %d: v1=%#v v2=%#v", index, v1[index], v2[index])
		}
		if index > 0 && v2[index-1].ID >= v2[index].ID {
			t.Fatalf("v2 manifests are not sorted: %q before %q", v2[index-1].ID, v2[index].ID)
		}
	}
	manifest, ok, err := FindBuiltinManifestV2("directory-services")
	if err != nil || !ok || manifest.ID != "directory-services" {
		t.Fatalf("FindBuiltinManifestV2() = %#v, %v, %v", manifest, ok, err)
	}
	if _, ok, err := FindBuiltinManifestV2("missing"); err != nil || ok {
		t.Fatalf("missing FindBuiltinManifestV2() = %v, %v", ok, err)
	}
}

func TestMigrationMetadataValidation(t *testing.T) {
	manifest, err := MigrateBuiltinManifestV1(BuiltinManifest{
		ID: "legacy", Version: "1.0.0", Capabilities: []string{"legacy"}, Platforms: []string{"linux"}, Lifecycle: []LifecycleOperation{LifecycleInstall},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*ManifestMigration)
	}{
		{name: "source", edit: func(m *ManifestMigration) { m.SourceSchemaVersion = "market.manifest/v0" }},
		{name: "strategy", edit: func(m *ManifestMigration) { m.Strategy = "INFER" }},
		{name: "warnings missing", edit: func(m *ManifestMigration) { m.Warnings = nil }},
		{name: "warning duplicate", edit: func(m *ManifestMigration) { m.Warnings = []string{"gap", "gap"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyOfManifest := manifest
			migration := *manifest.Migration
			migration.Warnings = append([]string(nil), migration.Warnings...)
			copyOfManifest.Migration = &migration
			test.edit(copyOfManifest.Migration)
			if err := ValidateManifestV2(copyOfManifest); err == nil {
				t.Fatal("invalid migration metadata accepted")
			}
		})
	}
}

func removeLifecycleOperation(operations []LifecycleOperationV2, target LifecycleOperationV2) []LifecycleOperationV2 {
	result := make([]LifecycleOperationV2, 0, len(operations))
	for _, operation := range operations {
		if operation != target {
			result = append(result, operation)
		}
	}
	return result
}
