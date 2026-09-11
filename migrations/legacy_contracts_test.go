package migrations

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
	"control-center/internal/market"
)

func TestLegacyNodeMarketAndCoreContractMigrationIsDeterministic(t *testing.T) {
	legacyNode := agent.EnrollmentRequest{
		NodeID: " node-legacy ", Hostname: " legacy.example.test ",
		Capabilities: []string{"PXE", "inventory", "pxe"},
	}
	firstNode, err := agent.NormalizeEnrollmentContract(legacyNode)
	if err != nil {
		t.Fatal(err)
	}
	secondNode, err := agent.NormalizeEnrollmentContract(legacyNode)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstNode, secondNode) {
		t.Fatal("legacy Node compatibility migration is not deterministic")
	}
	if firstNode.NodeID != "node-legacy" || firstNode.Hostname != "legacy.example.test" ||
		!reflect.DeepEqual(firstNode.Capabilities, []string{"inventory", "pxe"}) {
		t.Fatalf("legacy Node compatibility result is not canonical: %#v", firstNode.EnrollmentRequest)
	}
	if firstNode.Preconditions.Ready || firstNode.Effects.PersistsEnrollment || firstNode.Effects.NetworkMutation {
		t.Fatal("legacy Node compatibility path inferred v2 evidence or produced side effects")
	}

	legacyMarket := market.BuiltinManifest{
		ID: "legacy-module", Version: "1.2.3",
		Capabilities: []string{"zeta", "alpha", "zeta"},
		Platforms:    []string{"windows", "linux", "linux"},
		Providers:    []string{"provider-b", "provider-a"},
		Lifecycle:    []market.LifecycleOperation{market.LifecycleRemove, market.LifecycleInstall, market.LifecycleUpgrade},
	}
	firstMarket, err := market.MigrateBuiltinManifestV1(legacyMarket)
	if err != nil {
		t.Fatal(err)
	}
	secondMarket, err := market.MigrateBuiltinManifestV1(legacyMarket)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(firstMarket)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(secondMarket)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("legacy Market manifest migration is not byte-deterministic")
	}
	if firstMarket.Status != market.ManifestStatusTransitional ||
		firstMarket.Capacity.Status != market.CapacityUnverified ||
		firstMarket.Recovery.Backup != market.RecoveryUndeclared ||
		firstMarket.Activation.Mode != market.ActivationExplicit {
		t.Fatal("legacy Market migration inferred unsupported readiness or capabilities")
	}

	createdAt := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	firstCore := corecontracts.LegacyGlobalScopeObject(createdAt)
	secondCore := corecontracts.LegacyGlobalScopeObject(createdAt)
	if !reflect.DeepEqual(firstCore, secondCore) {
		t.Fatal("legacy distributed-core bootstrap migration is not deterministic")
	}
	if err := corecontracts.ValidateStoredObjects([]corecontracts.StoredObject{firstCore}); err != nil {
		t.Fatalf("legacy distributed-core bootstrap is invalid: %v", err)
	}
}
