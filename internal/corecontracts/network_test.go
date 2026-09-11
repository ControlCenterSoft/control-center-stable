package corecontracts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNetworkContractEnumsAreClosed(t *testing.T) {
	zoneKinds := []NetworkZoneKind{
		NetworkZoneUnassigned,
		NetworkZoneWAN,
		NetworkZoneLAN,
		NetworkZoneManagement,
		NetworkZoneDMZ,
		NetworkZoneCluster,
		NetworkZoneStorage,
		NetworkZoneBackup,
		NetworkZoneTrusted,
	}
	for _, kind := range zoneKinds {
		if !kind.Valid() {
			t.Errorf("canonical Network Zone kind %q is invalid", kind)
		}
	}
	if NetworkZoneKind("internet").Valid() {
		t.Fatal("unknown Network Zone kind was accepted")
	}

	interfaceKinds := []NetworkInterfaceKind{
		NetworkInterfacePhysical,
		NetworkInterfaceBond,
		NetworkInterfaceBridge,
		NetworkInterfaceVLAN,
		NetworkInterfaceVirtual,
	}
	for _, kind := range interfaceKinds {
		if !kind.Valid() {
			t.Errorf("canonical Network Interface kind %q is invalid", kind)
		}
	}
	if NetworkInterfaceKind("tunnel").Valid() {
		t.Fatal("unknown Network Interface kind was accepted")
	}
	for _, state := range []NetworkLinkState{NetworkLinkUp, NetworkLinkDown, NetworkLinkUnknown} {
		if !state.Valid() {
			t.Errorf("canonical Network Link state %q is invalid", state)
		}
	}
	if NetworkLinkState("enabled").Valid() {
		t.Fatal("unknown Network Link state was accepted")
	}
}

func TestNetworkContractsPersistThroughGenericRepository(t *testing.T) {
	repository := newDeterministicObjectRepository(t)
	applyNetworkTopologyFixture(t, repository)

	zone := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkZone,
		ObjectID: "net-zone-lan", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"net-zone-lan","name":"Site A LAN","kind":"lan","scope_id":"scope-site-a","site_id":"site-a"}`),
	})
	if zone.Generation != 1 {
		t.Fatalf("network zone generation=%d, want 1", zone.Generation)
	}
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-eth0", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-eth0","node_id":"node-a","name":"eth0","kind":"physical","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","mac_address":"02:00:00:00:00:01","operational_state":"up","addresses":["192.0.2.10/24"],"mtu":1500,"link_speed_mbps":1000}`),
	})
	vlan := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-vlan100", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-vlan100","node_id":"node-a","name":"eth0.100","kind":"vlan","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"down","addresses":[],"mtu":1500,"parent_interface_id":"nic-node-a-eth0","vlan_id":100}`),
	})

	interfaces, err := repository.List(context.Background(), ObjectFilter{ObjectType: ObjectNetworkInterface})
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 2 || interfaces[0].ObjectID != "nic-node-a-eth0" || interfaces[1].ObjectID != "nic-node-a-vlan100" {
		t.Fatalf("network interface listing=%#v", interfaces)
	}
	loaded, err := repository.Get(context.Background(), vlan.ObjectID)
	if err != nil || loaded.ResourceVersion != vlan.ResourceVersion {
		t.Fatalf("get VLAN: object=%#v err=%v", loaded, err)
	}

	updated := applyObject(t, repository, MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectNetworkInterface,
		ObjectID: vlan.ObjectID, ScopeID: vlan.ScopeID, OwnerScope: vlan.OwnerScope,
		Document:     json.RawMessage(`{"id":"nic-node-a-vlan100","node_id":"node-a","name":"eth0.100","kind":"vlan","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","addresses":["198.51.100.10/24"],"mtu":1500,"parent_interface_id":"nic-node-a-eth0","vlan_id":100}`),
		Precondition: objectPrecondition(vlan),
	})
	if updated.Generation != vlan.Generation+1 || updated.ResourceVersion == vlan.ResourceVersion {
		t.Fatalf("network interface CAS metadata: before=%#v after=%#v", vlan.ObjectMetadata, updated.ObjectMetadata)
	}
}

func TestNetworkContractRejectsUnsafeAndDanglingDocuments(t *testing.T) {
	tests := []struct {
		name       string
		objectType ObjectType
		document   string
	}{
		{
			name:       "unknown zone kind",
			objectType: ObjectNetworkZone,
			document:   `{"id":"net-zone-lan","name":"LAN","kind":"internet","scope_id":"scope-site-a","site_id":"site-a"}`,
		},
		{
			name:       "unknown site",
			objectType: ObjectNetworkZone,
			document:   `{"id":"net-zone-lan","name":"LAN","kind":"lan","scope_id":"scope-site-a","site_id":"missing"}`,
		},
		{
			name:       "network mutation directive",
			objectType: ObjectNetworkZone,
			document:   `{"id":"net-zone-lan","name":"LAN","kind":"lan","scope_id":"scope-site-a","site_id":"site-a","ip_forwarding":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newDeterministicObjectRepository(t)
			applyNetworkTopologyFixture(t, repository)
			_, err := repository.Apply(context.Background(), MutationRequest{
				Operation: MutationCreate, ObjectType: test.objectType,
				ObjectID: "net-zone-lan", ScopeID: "scope-site-a", OwnerScope: "global",
				Document: json.RawMessage(test.document),
			}, nextMutationKey())
			if !errors.Is(err, ErrInvalidObject) {
				t.Fatalf("Apply() error=%v, want ErrInvalidObject", err)
			}
		})
	}

	validInterface := `{"id":"nic-node-a-eth0","node_id":"node-a","name":"eth0","kind":"physical","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","mac_address":"02:00:00:00:00:01","operational_state":"up","addresses":["192.0.2.10/24"],"mtu":1500}`
	interfaceTests := []struct {
		name     string
		document string
	}{
		{name: "unknown node", document: replaceJSONField(validInterface, `"node_id":"node-a"`, `"node_id":"node-missing"`)},
		{name: "unknown zone", document: replaceJSONField(validInterface, `"network_zone_id":"net-zone-lan"`, `"network_zone_id":"missing"`)},
		{name: "noncanonical mac", document: replaceJSONField(validInterface, `"mac_address":"02:00:00:00:00:01"`, `"mac_address":"02-00-00-00-00-01"`)},
		{name: "invalid cidr", document: replaceJSONField(validInterface, `"192.0.2.10/24"`, `"192.0.2.10"`)},
		{name: "physical without mac", document: replaceJSONField(validInterface, `,"mac_address":"02:00:00:00:00:01"`, ``)},
		{name: "vlan without parent", document: replaceJSONField(replaceJSONField(validInterface, `"kind":"physical"`, `"kind":"vlan"`), `,"mac_address":"02:00:00:00:00:01"`, `,"vlan_id":100`)},
		{name: "unsafe route field", document: replaceJSONField(validInterface, `"mtu":1500`, `"mtu":1500,"default_route":"192.0.2.1"`)},
	}
	for _, test := range interfaceTests {
		t.Run(test.name, func(t *testing.T) {
			repository := repositoryWithNetworkZone(t)
			_, err := repository.Apply(context.Background(), MutationRequest{
				Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
				ObjectID: "nic-node-a-eth0", ScopeID: "scope-site-a", OwnerScope: "global",
				Document: json.RawMessage(test.document),
			}, nextMutationKey())
			if !errors.Is(err, ErrInvalidObject) {
				t.Fatalf("Apply() error=%v, want ErrInvalidObject", err)
			}
		})
	}
}

func TestNetworkContractEnforcesParentGraphAndImmutableIdentity(t *testing.T) {
	repository := repositoryWithNetworkZone(t)
	parent := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-eth0", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-eth0","node_id":"node-a","name":"eth0","kind":"physical","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","mac_address":"02:00:00:00:00:01","operational_state":"up","mtu":1500}`),
	})
	child := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-vlan100", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-vlan100","node_id":"node-a","name":"eth0.100","kind":"vlan","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","mtu":1500,"parent_interface_id":"nic-node-a-eth0","vlan_id":100}`),
	})
	_, err := repository.Apply(context.Background(), MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-vlan100-duplicate", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-vlan100-duplicate","node_id":"node-a","name":"duplicate.100","kind":"vlan","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","mtu":1500,"parent_interface_id":"nic-node-a-eth0","vlan_id":100}`),
	}, nextMutationKey())
	if !errors.Is(err, ErrInvalidObject) || !errors.Is(err, ErrInvalidNetworkContract) {
		t.Fatalf("duplicate VLAN binding error=%v, want ErrInvalidObject and ErrInvalidNetworkContract", err)
	}

	_, err = repository.Apply(context.Background(), MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectNetworkInterface,
		ObjectID: parent.ObjectID, ScopeID: parent.ScopeID, OwnerScope: parent.OwnerScope,
		Document:     json.RawMessage(`{"id":"nic-node-a-eth0","node_id":"node-a","name":"eth0","kind":"virtual","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","mac_address":"02:00:00:00:00:01","operational_state":"up","mtu":1500,"parent_interface_id":"nic-node-a-vlan100"}`),
		Precondition: objectPrecondition(parent),
	}, nextMutationKey())
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("kind change error=%v, want ErrInvalidTransition", err)
	}

	_, err = repository.Apply(context.Background(), MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectNetworkInterface,
		ObjectID: child.ObjectID, ScopeID: child.ScopeID, OwnerScope: child.OwnerScope,
		Document:     json.RawMessage(`{"id":"nic-node-a-vlan100","node_id":"node-b","name":"eth0.100","kind":"vlan","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","mtu":1500,"parent_interface_id":"nic-node-a-eth0","vlan_id":100}`),
		Precondition: objectPrecondition(child),
	}, nextMutationKey())
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("node change error=%v, want ErrInvalidTransition", err)
	}
}

func TestNetworkContractRejectsParentCycle(t *testing.T) {
	repository := repositoryWithNetworkZone(t)
	first := applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-virtual-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-virtual-a","node_id":"node-a","name":"virtual-a","kind":"virtual","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","mtu":1500}`),
	})
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-virtual-b", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-virtual-b","node_id":"node-a","name":"virtual-b","kind":"virtual","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","mtu":1500,"parent_interface_id":"nic-node-a-virtual-a"}`),
	})

	_, err := repository.Apply(context.Background(), MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectNetworkInterface,
		ObjectID: first.ObjectID, ScopeID: first.ScopeID, OwnerScope: first.OwnerScope,
		Document:     json.RawMessage(`{"id":"nic-node-a-virtual-a","node_id":"node-a","name":"virtual-a","kind":"virtual","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","operational_state":"up","mtu":1500,"parent_interface_id":"nic-node-a-virtual-b"}`),
		Precondition: objectPrecondition(first),
	}, nextMutationKey())
	if !errors.Is(err, ErrInvalidObject) || !errors.Is(err, ErrInvalidNetworkContract) {
		t.Fatalf("parent cycle error=%v, want ErrInvalidObject and ErrInvalidNetworkContract", err)
	}
}

func TestNetworkContractPreventsRoleMoveThatOrphansInterface(t *testing.T) {
	repository := repositoryWithNetworkZone(t)
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkInterface,
		ObjectID: "nic-node-a-eth0", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"nic-node-a-eth0","node_id":"node-a","name":"eth0","kind":"physical","scope_id":"scope-site-a","site_id":"site-a","network_zone_id":"net-zone-lan","mac_address":"02:00:00:00:00:01","operational_state":"up","mtu":1500}`),
	})
	role, err := repository.Get(context.Background(), "role-agent-node-a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Apply(context.Background(), MutationRequest{
		Operation: MutationReplace, ObjectType: ObjectRoleAssignment,
		ObjectID: role.ObjectID, ScopeID: role.ScopeID, OwnerScope: role.OwnerScope,
		Document:     json.RawMessage(`{"target_node_id":"node-b","service_identity_id":"agent-node-a","role":"agent","site_id":"site-a"}`),
		Precondition: objectPrecondition(role),
	}, nextMutationKey())
	if !errors.Is(err, ErrInvalidObject) || !errors.Is(err, ErrInvalidNetworkContract) {
		t.Fatalf("orphaning role move error=%v, want ErrInvalidObject and ErrInvalidNetworkContract", err)
	}
}

func applyNetworkTopologyFixture(t *testing.T, repository ObjectRepository) {
	t.Helper()
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectScope,
		ObjectID: "scope-site-a", ScopeID: "global", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"scope-site-a","kind":"site","name":"Site A scope","parent_id":"global","delegated_authorities":["configuration","desired-state"]}`),
	})
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectSite,
		ObjectID: "site-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"site-a","name":"Site A","scope_id":"scope-site-a"}`),
	})
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectRoleAssignment,
		ObjectID: "role-agent-node-a", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"target_node_id":"node-a","service_identity_id":"agent-node-a","role":"agent","site_id":"site-a"}`),
	})
}

func repositoryWithNetworkZone(t *testing.T) *MemoryObjectRepository {
	t.Helper()
	repository := newDeterministicObjectRepository(t)
	applyNetworkTopologyFixture(t, repository)
	applyObject(t, repository, MutationRequest{
		Operation: MutationCreate, ObjectType: ObjectNetworkZone,
		ObjectID: "net-zone-lan", ScopeID: "scope-site-a", OwnerScope: "global",
		Document: json.RawMessage(`{"id":"net-zone-lan","name":"Site A LAN","kind":"lan","scope_id":"scope-site-a","site_id":"site-a"}`),
	})
	return repository
}

func replaceJSONField(document, oldValue, newValue string) string {
	return strings.Replace(document, oldValue, newValue, 1)
}
