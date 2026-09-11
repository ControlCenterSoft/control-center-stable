// Package edgegateway defines the side-effect-free authorization contract for
// Control Center Edge Gateway plans. It never changes host networking.
package edgegateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"control-center/internal/corecontracts"
	"control-center/internal/networkpolicy"
)

const (
	RequestSchemaVersion = "network.edge-gateway.authorization-request/v1"
	PlanSchemaVersion    = "network.edge-gateway.authorization-plan/v1"
	MaxRequestBytes      = 16 * 1024
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$`)

// DenyCode is a stable, machine-readable fail-closed outcome. Callers must not
// branch on the human-readable error text.
type DenyCode string

const (
	DenyRequestInvalid             DenyCode = "EDGE_REQUEST_INVALID"
	DenyOperationExplicitRequired  DenyCode = "EDGE_OPERATION_EXPLICIT_REQUIRED"
	DenyOperationUnsupported       DenyCode = "EDGE_OPERATION_UNSUPPORTED"
	DenyPlanOnly                   DenyCode = "EDGE_PLAN_ONLY"
	DenyEdgeRoleRequired           DenyCode = "EDGE_ROLE_REQUIRED"
	DenyApprovalRequired           DenyCode = "EDGE_APPROVAL_REQUIRED"
	DenyCapabilityRequired         DenyCode = "EDGE_CAPABILITY_REQUIRED"
	DenyDistinctInterfacesRequired DenyCode = "EDGE_DISTINCT_INTERFACES_REQUIRED"
	DenyInterfaceNotFound          DenyCode = "EDGE_INTERFACE_NOT_FOUND"
	DenyInterfaceBoundaryMismatch  DenyCode = "EDGE_INTERFACE_BOUNDARY_MISMATCH"
	DenyZoneNotFound               DenyCode = "EDGE_ZONE_NOT_FOUND"
	DenyZoneBoundaryMismatch       DenyCode = "EDGE_ZONE_BOUNDARY_MISMATCH"
	DenyWANZoneRequired            DenyCode = "EDGE_WAN_ZONE_REQUIRED"
	DenyLANZoneRequired            DenyCode = "EDGE_LAN_ZONE_REQUIRED"
	DenyInventoryInvalid           DenyCode = "EDGE_INVENTORY_INVALID"
	DenyNetworkSafetyPolicy        DenyCode = "EDGE_NETWORK_SAFETY_POLICY"
)

// Denial retains a stable code while keeping details suitable for operators.
type Denial struct {
	Code   DenyCode `json:"code"`
	Detail string   `json:"detail"`
}

func (d *Denial) Error() string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", d.Code, d.Detail)
}

func deny(code DenyCode, format string, args ...any) error {
	return &Denial{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// CodeOf extracts the stable deny code without requiring callers to expose
// error text in an API response.
func CodeOf(err error) (DenyCode, bool) {
	var denial *Denial
	if !errors.As(err, &denial) {
		return "", false
	}
	return denial.Code, true
}

type Mode string

const ModePlan Mode = "plan"

type Operation string

const (
	OperationRouting        Operation = "routing"
	OperationNAT            Operation = "nat"
	OperationPortForwarding Operation = "port-forwarding"
)

type Capability string

const (
	CapabilityRoutingPlan        Capability = "network.edge.routing.plan"
	CapabilityNATPlan            Capability = "network.edge.nat.plan"
	CapabilityPortForwardingPlan Capability = "network.edge.port-forwarding.plan"
)

type ApprovalDecision string

const ApprovalApproved ApprovalDecision = "approved"

// Approval is an opaque reference to a decision already made by the policy
// engine. It carries no credentials, actor data, or executable content.
type Approval struct {
	ID       string           `json:"id"`
	PolicyID string           `json:"policy_id"`
	Decision ApprovalDecision `json:"decision"`
}

type Target struct {
	NodeID         string `json:"node_id"`
	ScopeID        string `json:"scope_id"`
	SiteID         string `json:"site_id"`
	WANInterfaceID string `json:"wan_interface_id"`
	LANInterfaceID string `json:"lan_interface_id"`
}

// AuthorizationRequest must name one operation. There is deliberately no
// automatic/discovered operation and no execution mode.
type AuthorizationRequest struct {
	SchemaVersion string     `json:"schema_version"`
	Mode          Mode       `json:"mode"`
	Operation     Operation  `json:"operation"`
	Target        Target     `json:"target"`
	Approval      Approval   `json:"approval"`
	Capability    Capability `json:"capability"`
}

// Inventory is a caller-supplied coherent snapshot. This package consumes the
// existing distributed Core contracts without creating another topology model.
type Inventory struct {
	RoleAssignments []corecontracts.RoleAssignment
	Zones           []corecontracts.NetworkZone
	Interfaces      []corecontracts.NetworkInterface
}

type Evidence struct {
	EdgeRoleAssignmentID    string     `json:"edge_role_assignment_id"`
	EdgeRoleResourceVersion string     `json:"edge_role_resource_version"`
	WANInterfaceID          string     `json:"wan_interface_id"`
	WANZoneID               string     `json:"wan_zone_id"`
	LANInterfaceID          string     `json:"lan_interface_id"`
	LANZoneID               string     `json:"lan_zone_id"`
	ApprovalID              string     `json:"approval_id"`
	ApprovalPolicyID        string     `json:"approval_policy_id"`
	Capability              Capability `json:"capability"`
}

type PlanStep string

const (
	StepValidateEdgeRole   PlanStep = "validate-edge-role"
	StepValidateApproval   PlanStep = "validate-explicit-approval"
	StepValidateCapability PlanStep = "validate-operation-capability"
	StepValidateWANLAN     PlanStep = "validate-distinct-wan-lan-interfaces"
	StepEmitAuthorizedPlan PlanStep = "emit-authorized-plan"
)

// AuthorizationPlan is an immutable proposal, not a desired-state or worker
// instruction. ProductionMutationEnabled is always false in this release.
type AuthorizationPlan struct {
	SchemaVersion             string     `json:"schema_version"`
	PlanID                    string     `json:"plan_id"`
	Mode                      Mode       `json:"mode"`
	Operation                 Operation  `json:"operation"`
	Target                    Target     `json:"target"`
	RequiredCapability        Capability `json:"required_capability"`
	Evidence                  Evidence   `json:"evidence"`
	Steps                     []PlanStep `json:"steps"`
	Authorized                bool       `json:"authorized"`
	ProductionMutationEnabled bool       `json:"production_mutation_enabled"`
}

// DecodeRequest decodes exactly one bounded JSON document and rejects unknown
// fields so an execution directive can never be silently ignored.
func DecodeRequest(reader io.Reader) (AuthorizationRequest, error) {
	if reader == nil {
		return AuthorizationRequest{}, deny(DenyRequestInvalid, "request body is required")
	}
	limited := io.LimitReader(reader, MaxRequestBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return AuthorizationRequest{}, deny(DenyRequestInvalid, "request body cannot be read")
	}
	if len(body) == 0 || len(body) > MaxRequestBytes {
		return AuthorizationRequest{}, deny(DenyRequestInvalid, "request body must contain at most %d bytes", MaxRequestBytes)
	}
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return AuthorizationRequest{}, deny(DenyRequestInvalid, "request body must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request AuthorizationRequest
	if err := decoder.Decode(&request); err != nil {
		return AuthorizationRequest{}, deny(DenyRequestInvalid, "request body must match the authorization contract")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return AuthorizationRequest{}, deny(DenyRequestInvalid, "request body must contain exactly one JSON document")
	}
	return request, nil
}

// BuildAuthorizationPlan evaluates explicit role, approval, capability and
// multi-NIC WAN/LAN evidence, then emits a deterministic plan. It has no host,
// persistence, job, forwarding, NAT, firewall, or port-forwarding side effect.
func BuildAuthorizationPlan(request AuthorizationRequest, inventory Inventory) (AuthorizationPlan, error) {
	requiredCapability, err := validateRequest(request)
	if err != nil {
		return AuthorizationPlan{}, err
	}

	assignment, err := explicitEdgeAssignment(request.Target, inventory.RoleAssignments)
	if err != nil {
		return AuthorizationPlan{}, err
	}
	if assignment.ObjectID == "" {
		return AuthorizationPlan{}, deny(DenyEdgeRoleRequired, "target node has no valid explicit Edge Gateway role assignment in the requested site and scope")
	}
	if !validIdentifier(request.Approval.ID) || !validIdentifier(request.Approval.PolicyID) || request.Approval.Decision != ApprovalApproved {
		return AuthorizationPlan{}, deny(DenyApprovalRequired, "an explicit approved policy decision is required")
	}
	if request.Capability != requiredCapability {
		return AuthorizationPlan{}, deny(DenyCapabilityRequired, "operation requires capability %q", requiredCapability)
	}
	if request.Target.WANInterfaceID == request.Target.LANInterfaceID {
		return AuthorizationPlan{}, deny(DenyDistinctInterfacesRequired, "WAN and LAN interfaces must be distinct")
	}

	interfaces, err := indexInterfaces(inventory.Interfaces)
	if err != nil {
		return AuthorizationPlan{}, err
	}
	wanInterface, wanFound := interfaces[request.Target.WANInterfaceID]
	lanInterface, lanFound := interfaces[request.Target.LANInterfaceID]
	if !wanFound || !lanFound {
		return AuthorizationPlan{}, deny(DenyInterfaceNotFound, "both explicitly named interfaces must exist")
	}
	if !interfaceMatchesTarget(wanInterface, request.Target) || !interfaceMatchesTarget(lanInterface, request.Target) {
		return AuthorizationPlan{}, deny(DenyInterfaceBoundaryMismatch, "interfaces must belong to the target node, site, and scope")
	}
	wanRoot, err := interfaceRoot(wanInterface, interfaces)
	if err != nil {
		return AuthorizationPlan{}, err
	}
	lanRoot, err := interfaceRoot(lanInterface, interfaces)
	if err != nil {
		return AuthorizationPlan{}, err
	}
	if wanRoot == lanRoot {
		return AuthorizationPlan{}, deny(DenyDistinctInterfacesRequired, "WAN and LAN interfaces must use distinct interface paths")
	}

	zones, err := indexZones(inventory.Zones)
	if err != nil {
		return AuthorizationPlan{}, err
	}
	wanZone, wanFound := zones[wanInterface.NetworkZoneID]
	lanZone, lanFound := zones[lanInterface.NetworkZoneID]
	if !wanFound || !lanFound {
		return AuthorizationPlan{}, deny(DenyZoneNotFound, "both interface zones must exist")
	}
	if !zoneMatchesTarget(wanZone, request.Target) || !zoneMatchesTarget(lanZone, request.Target) {
		return AuthorizationPlan{}, deny(DenyZoneBoundaryMismatch, "zones must belong to the requested site and scope")
	}
	if wanZone.Kind != corecontracts.NetworkZoneWAN {
		return AuthorizationPlan{}, deny(DenyWANZoneRequired, "wan_interface_id must reference a WAN zone")
	}
	if lanZone.Kind != corecontracts.NetworkZoneLAN {
		return AuthorizationPlan{}, deny(DenyLANZoneRequired, "lan_interface_id must reference a LAN zone")
	}

	if err := networkpolicy.AuthorizeForwarding(networkpolicy.ForwardingIntent{
		Source:              networkpolicy.ZoneWAN,
		Destination:         networkpolicy.ZoneLAN,
		ExplicitlyEnabled:   true,
		EdgeGatewayAssigned: true,
	}); err != nil {
		return AuthorizationPlan{}, deny(DenyNetworkSafetyPolicy, "network forwarding safety baseline denied the plan")
	}

	plan := AuthorizationPlan{
		SchemaVersion:      PlanSchemaVersion,
		Mode:               ModePlan,
		Operation:          request.Operation,
		Target:             request.Target,
		RequiredCapability: requiredCapability,
		Evidence: Evidence{
			EdgeRoleAssignmentID:    assignment.ObjectID,
			EdgeRoleResourceVersion: assignment.ResourceVersion,
			WANInterfaceID:          wanInterface.ID,
			WANZoneID:               wanZone.ID,
			LANInterfaceID:          lanInterface.ID,
			LANZoneID:               lanZone.ID,
			ApprovalID:              request.Approval.ID,
			ApprovalPolicyID:        request.Approval.PolicyID,
			Capability:              request.Capability,
		},
		Steps: []PlanStep{
			StepValidateEdgeRole,
			StepValidateApproval,
			StepValidateCapability,
			StepValidateWANLAN,
			StepEmitAuthorizedPlan,
		},
		Authorized:                true,
		ProductionMutationEnabled: false,
	}
	plan.PlanID = planID(plan)
	return plan, nil
}

func validateRequest(request AuthorizationRequest) (Capability, error) {
	if request.SchemaVersion != RequestSchemaVersion {
		return "", deny(DenyRequestInvalid, "schema_version must be %q", RequestSchemaVersion)
	}
	if request.Operation == "" || request.Operation == "auto" || request.Operation == "automatic" {
		return "", deny(DenyOperationExplicitRequired, "routing, NAT, or port-forwarding must be selected explicitly")
	}
	var capability Capability
	switch request.Operation {
	case OperationRouting:
		capability = CapabilityRoutingPlan
	case OperationNAT:
		capability = CapabilityNATPlan
	case OperationPortForwarding:
		capability = CapabilityPortForwardingPlan
	default:
		return "", deny(DenyOperationUnsupported, "operation %q is not supported", request.Operation)
	}
	if request.Mode != ModePlan {
		return "", deny(DenyPlanOnly, "only side-effect-free plan mode is available")
	}
	for name, value := range map[string]string{
		"target.node_id":          request.Target.NodeID,
		"target.scope_id":         request.Target.ScopeID,
		"target.site_id":          request.Target.SiteID,
		"target.wan_interface_id": request.Target.WANInterfaceID,
		"target.lan_interface_id": request.Target.LANInterfaceID,
	} {
		if !validIdentifier(value) {
			return "", deny(DenyRequestInvalid, "%s is not a valid identifier", name)
		}
	}
	return capability, nil
}

func explicitEdgeAssignment(target Target, assignments []corecontracts.RoleAssignment) (corecontracts.RoleAssignment, error) {
	var match corecontracts.RoleAssignment
	for _, assignment := range assignments {
		if assignment.Role != corecontracts.RoleEdgeGateway ||
			assignment.TargetNodeID != target.NodeID ||
			assignment.ScopeID != target.ScopeID ||
			assignment.SiteID != target.SiteID ||
			assignment.ObjectMetadata.Validate() != nil ||
			!validIdentifier(assignment.ServiceIdentityID) {
			continue
		}
		if match.ObjectID != "" {
			return corecontracts.RoleAssignment{}, deny(DenyInventoryInvalid, "multiple Edge Gateway assignments match the target boundary")
		}
		match = assignment
	}
	return match, nil
}

func indexInterfaces(values []corecontracts.NetworkInterface) (map[string]corecontracts.NetworkInterface, error) {
	result := make(map[string]corecontracts.NetworkInterface, len(values))
	for _, networkInterface := range values {
		if !validIdentifier(networkInterface.ID) || !validIdentifier(networkInterface.NetworkZoneID) {
			return nil, deny(DenyInventoryInvalid, "network interface inventory contains an invalid identifier")
		}
		if _, exists := result[networkInterface.ID]; exists {
			return nil, deny(DenyInventoryInvalid, "network interface inventory contains duplicate IDs")
		}
		result[networkInterface.ID] = networkInterface
	}
	return result, nil
}

func indexZones(values []corecontracts.NetworkZone) (map[string]corecontracts.NetworkZone, error) {
	result := make(map[string]corecontracts.NetworkZone, len(values))
	for _, zone := range values {
		if !validIdentifier(zone.ID) || !zone.Kind.Valid() {
			return nil, deny(DenyInventoryInvalid, "network zone inventory contains an invalid record")
		}
		if _, exists := result[zone.ID]; exists {
			return nil, deny(DenyInventoryInvalid, "network zone inventory contains duplicate IDs")
		}
		result[zone.ID] = zone
	}
	return result, nil
}

func interfaceRoot(networkInterface corecontracts.NetworkInterface, interfaces map[string]corecontracts.NetworkInterface) (string, error) {
	seen := make(map[string]struct{}, len(interfaces))
	current := networkInterface
	for {
		if _, exists := seen[current.ID]; exists {
			return "", deny(DenyInventoryInvalid, "network interface inventory contains a parent cycle")
		}
		seen[current.ID] = struct{}{}
		if current.ParentInterfaceID == "" {
			return current.ID, nil
		}
		parent, exists := interfaces[current.ParentInterfaceID]
		if !exists {
			return "", deny(DenyInventoryInvalid, "network interface inventory contains a missing parent")
		}
		if parent.NodeID != current.NodeID || parent.SiteID != current.SiteID || parent.ScopeID != current.ScopeID {
			return "", deny(DenyInventoryInvalid, "network interface parent crosses a node, site, or scope boundary")
		}
		current = parent
	}
}

func interfaceMatchesTarget(networkInterface corecontracts.NetworkInterface, target Target) bool {
	return networkInterface.NodeID == target.NodeID &&
		networkInterface.SiteID == target.SiteID &&
		networkInterface.ScopeID == target.ScopeID
}

func zoneMatchesTarget(zone corecontracts.NetworkZone, target Target) bool {
	return zone.SiteID == target.SiteID && zone.ScopeID == target.ScopeID
}

func validIdentifier(value string) bool {
	return len(value) <= 255 && identifierPattern.MatchString(value)
}

func planID(plan AuthorizationPlan) string {
	copy := plan
	copy.PlanID = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		panic("edge gateway plan contains non-serializable fields")
	}
	digest := sha256.Sum256(payload)
	return "edge-plan:" + hex.EncodeToString(digest[:])
}
