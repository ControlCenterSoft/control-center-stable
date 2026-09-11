package edgegateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func TestBuildAuthorizationPlanAllowsEveryExplicitOperation(t *testing.T) {
	tests := []struct {
		operation  Operation
		capability Capability
	}{
		{OperationRouting, CapabilityRoutingPlan},
		{OperationNAT, CapabilityNATPlan},
		{OperationPortForwarding, CapabilityPortForwardingPlan},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			request, inventory := validFixture()
			request.Operation = test.operation
			request.Capability = test.capability

			first, err := BuildAuthorizationPlan(request, inventory)
			if err != nil {
				t.Fatal(err)
			}
			second, err := BuildAuthorizationPlan(request, reverseInventory(inventory))
			if err != nil {
				t.Fatal(err)
			}
			if first.PlanID == "" || first.PlanID != second.PlanID {
				t.Fatalf("plan IDs are not deterministic: %q %q", first.PlanID, second.PlanID)
			}
			if first.SchemaVersion != PlanSchemaVersion || first.Mode != ModePlan || first.Operation != test.operation {
				t.Fatalf("unexpected contract envelope: %#v", first)
			}
			if !first.Authorized || first.ProductionMutationEnabled {
				t.Fatalf("unsafe plan flags: %#v", first)
			}
			if first.RequiredCapability != test.capability || first.Evidence.Capability != test.capability {
				t.Fatalf("capability evidence=%#v, want %q", first.Evidence, test.capability)
			}
			wantSteps := []PlanStep{StepValidateEdgeRole, StepValidateApproval, StepValidateCapability, StepValidateWANLAN, StepEmitAuthorizedPlan}
			if !equalSteps(first.Steps, wantSteps) {
				t.Fatalf("steps=%#v, want %#v", first.Steps, wantSteps)
			}
		})
	}
}

func TestBuildAuthorizationPlanFailClosedDenyCodes(t *testing.T) {
	tests := []struct {
		name   string
		code   DenyCode
		mutate func(*AuthorizationRequest, *Inventory)
	}{
		{name: "schema missing", code: DenyRequestInvalid, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.SchemaVersion = "" }},
		{name: "operation missing", code: DenyOperationExplicitRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Operation = "" }},
		{name: "automatic operation", code: DenyOperationExplicitRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Operation = "auto" }},
		{name: "operation unsupported", code: DenyOperationUnsupported, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Operation = "firewall" }},
		{name: "execution requested", code: DenyPlanOnly, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Mode = "execute" }},
		{name: "target invalid", code: DenyRequestInvalid, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Target.NodeID = "bad node" }},
		{name: "edge role absent", code: DenyEdgeRoleRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.RoleAssignments = nil }},
		{name: "wrong role", code: DenyEdgeRoleRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.RoleAssignments[0].Role = corecontracts.RoleWorkerNode }},
		{name: "edge role different node", code: DenyEdgeRoleRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.RoleAssignments[0].TargetNodeID = "node-other" }},
		{name: "edge role different site", code: DenyEdgeRoleRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.RoleAssignments[0].SiteID = "site-other" }},
		{name: "edge role different scope", code: DenyEdgeRoleRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.RoleAssignments[0].ScopeID = "scope-other" }},
		{name: "edge role unpersisted", code: DenyEdgeRoleRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.RoleAssignments[0].Generation = 0 }},
		{name: "ambiguous edge role", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) {
			other := i.RoleAssignments[0]
			other.ObjectID = "role-edge-2"
			other.ResourceVersion = "rv:edge:2"
			i.RoleAssignments = append(i.RoleAssignments, other)
		}},
		{name: "approval missing", code: DenyApprovalRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Approval = Approval{} }},
		{name: "approval denied", code: DenyApprovalRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Approval.Decision = "denied" }},
		{name: "approval identifier invalid", code: DenyApprovalRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Approval.PolicyID = "bad policy" }},
		{name: "capability missing", code: DenyCapabilityRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Capability = "" }},
		{name: "capability from another operation", code: DenyCapabilityRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Capability = CapabilityNATPlan }},
		{name: "same interface", code: DenyDistinctInterfacesRequired, mutate: func(r *AuthorizationRequest, _ *Inventory) { r.Target.LANInterfaceID = r.Target.WANInterfaceID }},
		{name: "interface inventory duplicate", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces = append(i.Interfaces, i.Interfaces[0]) }},
		{name: "interface inventory invalid", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces[0].NetworkZoneID = "bad zone" }},
		{name: "wan interface missing", code: DenyInterfaceNotFound, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces = i.Interfaces[1:] }},
		{name: "lan interface missing", code: DenyInterfaceNotFound, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces = i.Interfaces[:1] }},
		{name: "interface wrong node", code: DenyInterfaceBoundaryMismatch, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces[0].NodeID = "node-other" }},
		{name: "interface wrong site", code: DenyInterfaceBoundaryMismatch, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces[1].SiteID = "site-other" }},
		{name: "interface wrong scope", code: DenyInterfaceBoundaryMismatch, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces[1].ScopeID = "scope-other" }},
		{name: "same physical path", code: DenyDistinctInterfacesRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) {
			parent := i.Interfaces[0]
			parent.ID = "nic-parent"
			parent.NetworkZoneID = "zone-wan"
			i.Interfaces[0].ParentInterfaceID = parent.ID
			i.Interfaces[1].ParentInterfaceID = parent.ID
			i.Interfaces = append(i.Interfaces, parent)
		}},
		{name: "parent missing", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Interfaces[0].ParentInterfaceID = "nic-missing" }},
		{name: "parent cycle", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) {
			i.Interfaces[0].ParentInterfaceID = i.Interfaces[1].ID
			i.Interfaces[1].ParentInterfaceID = i.Interfaces[0].ID
		}},
		{name: "parent crosses boundary", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) {
			parent := i.Interfaces[0]
			parent.ID = "nic-parent"
			parent.NodeID = "node-other"
			i.Interfaces[0].ParentInterfaceID = parent.ID
			i.Interfaces = append(i.Interfaces, parent)
		}},
		{name: "zone inventory duplicate", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones = append(i.Zones, i.Zones[0]) }},
		{name: "zone inventory invalid", code: DenyInventoryInvalid, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones[0].Kind = "internet" }},
		{name: "wan zone missing", code: DenyZoneNotFound, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones = i.Zones[1:] }},
		{name: "lan zone missing", code: DenyZoneNotFound, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones = i.Zones[:1] }},
		{name: "wan zone wrong site", code: DenyZoneBoundaryMismatch, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones[0].SiteID = "site-other" }},
		{name: "lan zone wrong scope", code: DenyZoneBoundaryMismatch, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones[1].ScopeID = "scope-other" }},
		{name: "wan kind wrong", code: DenyWANZoneRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones[0].Kind = corecontracts.NetworkZoneDMZ }},
		{name: "lan kind wrong", code: DenyLANZoneRequired, mutate: func(_ *AuthorizationRequest, i *Inventory) { i.Zones[1].Kind = corecontracts.NetworkZoneTrusted }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, inventory := validFixture()
			test.mutate(&request, &inventory)
			plan, err := BuildAuthorizationPlan(request, inventory)
			if err == nil {
				t.Fatalf("plan unexpectedly authorized: %#v", plan)
			}
			if code, ok := CodeOf(err); !ok || code != test.code {
				t.Fatalf("deny code=(%q,%t), want %q; error=%v", code, ok, test.code, err)
			}
			if !reflect.DeepEqual(plan, AuthorizationPlan{}) {
				t.Fatalf("denied request returned a partial plan: %#v", plan)
			}
		})
	}
}

func TestDecodeRequestIsBoundedStrictAndSingleDocument(t *testing.T) {
	request, _ := validFixture()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(bytes.NewReader(body))
	if err != nil || decoded != request {
		t.Fatalf("DecodeRequest()=%#v, %v", decoded, err)
	}

	tests := []struct {
		name string
		body []byte
	}{
		{name: "nil reader", body: nil},
		{name: "empty", body: []byte{}},
		{name: "null", body: []byte(`null`)},
		{name: "unknown execution field", body: []byte(`{"schema_version":"network.edge-gateway.authorization-request/v1","execution":true}`)},
		{name: "trailing document", body: append(append([]byte{}, body...), []byte(` {}`)...)},
		{name: "oversized", body: []byte(`{"padding":"` + strings.Repeat("x", MaxRequestBytes) + `"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reader io.Reader
			if test.body != nil {
				reader = bytes.NewReader(test.body)
			}
			_, err := DecodeRequest(reader)
			if code, ok := CodeOf(err); !ok || code != DenyRequestInvalid {
				t.Fatalf("error=%v code=(%q,%t), want %q", err, code, ok, DenyRequestInvalid)
			}
		})
	}
}

func TestAuthorizationPlanContainsNoExecutionOrSecretSurface(t *testing.T) {
	request, inventory := validFixture()
	plan, err := BuildAuthorizationPlan(request, inventory)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, forbidden := range []string{"command", "credential", "password", "secret", "token", "endpoint", "production_mutation_enabled\":true"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("plan exposes forbidden execution or secret surface %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"production_mutation_enabled":false`) {
		t.Fatalf("plan does not explicitly disable production mutation: %s", text)
	}
}

func TestCodeOfRejectsUnstructuredErrors(t *testing.T) {
	if code, ok := CodeOf(errors.New("plain error")); ok || code != "" {
		t.Fatalf("CodeOf()=(%q,%t), want empty false", code, ok)
	}
}

func validFixture() (AuthorizationRequest, Inventory) {
	now := time.Date(2026, time.September, 9, 9, 0, 0, 0, time.UTC)
	request := AuthorizationRequest{
		SchemaVersion: RequestSchemaVersion,
		Mode:          ModePlan,
		Operation:     OperationRouting,
		Target: Target{
			NodeID:         "node-edge-a",
			ScopeID:        "scope-site-a",
			SiteID:         "site-a",
			WANInterfaceID: "nic-wan-a",
			LANInterfaceID: "nic-lan-a",
		},
		Approval:   Approval{ID: "approval-edge-a", PolicyID: "edge-policy-v1", Decision: ApprovalApproved},
		Capability: CapabilityRoutingPlan,
	}
	inventory := Inventory{
		RoleAssignments: []corecontracts.RoleAssignment{{
			ObjectMetadata: corecontracts.ObjectMetadata{
				ObjectID: "role-edge-a", ScopeID: "scope-site-a", OwnerScope: "global",
				Generation: 1, ResourceVersion: "rv:edge:1", CreatedAt: now, UpdatedAt: now,
			},
			TargetNodeID: "node-edge-a", ServiceIdentityID: "edge-service-a",
			Role: corecontracts.RoleEdgeGateway, SiteID: "site-a",
		}},
		Zones: []corecontracts.NetworkZone{
			{ID: "zone-wan", Name: "WAN", Kind: corecontracts.NetworkZoneWAN, ScopeID: "scope-site-a", SiteID: "site-a"},
			{ID: "zone-lan", Name: "LAN", Kind: corecontracts.NetworkZoneLAN, ScopeID: "scope-site-a", SiteID: "site-a"},
		},
		Interfaces: []corecontracts.NetworkInterface{
			{ID: "nic-wan-a", NodeID: "node-edge-a", Name: "wan0", Kind: corecontracts.NetworkInterfacePhysical, ScopeID: "scope-site-a", SiteID: "site-a", NetworkZoneID: "zone-wan", OperationalState: corecontracts.NetworkLinkUp, MTU: 1500},
			{ID: "nic-lan-a", NodeID: "node-edge-a", Name: "lan0", Kind: corecontracts.NetworkInterfacePhysical, ScopeID: "scope-site-a", SiteID: "site-a", NetworkZoneID: "zone-lan", OperationalState: corecontracts.NetworkLinkUp, MTU: 1500},
		},
	}
	return request, inventory
}

func reverseInventory(inventory Inventory) Inventory {
	result := inventory
	result.RoleAssignments = reverseAssignments(inventory.RoleAssignments)
	result.Zones = reverseZones(inventory.Zones)
	result.Interfaces = reverseInterfaces(inventory.Interfaces)
	return result
}

func reverseAssignments(values []corecontracts.RoleAssignment) []corecontracts.RoleAssignment {
	result := append([]corecontracts.RoleAssignment(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reverseZones(values []corecontracts.NetworkZone) []corecontracts.NetworkZone {
	result := append([]corecontracts.NetworkZone(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reverseInterfaces(values []corecontracts.NetworkInterface) []corecontracts.NetworkInterface {
	result := append([]corecontracts.NetworkInterface(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func equalSteps(left, right []PlanStep) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
