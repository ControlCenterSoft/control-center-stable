package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/agent"
	"control-center/internal/corecontracts"
	"control-center/internal/inventory"
)

const InfrastructureInventoryContractVersion = "ui.infrastructure-inventory/v1"

var ErrInvalidInfrastructureInventory = errors.New("invalid infrastructure inventory view")

type InventoryViewState string

const (
	InventoryViewUnavailable InventoryViewState = "unavailable"
	InventoryViewCurrent     InventoryViewState = "current"
	InventoryViewStale       InventoryViewState = "stale"
	InventoryViewExpired     InventoryViewState = "expired"
)

type EvidenceAvailability string

const (
	EvidenceUnavailable EvidenceAvailability = "unavailable"
	EvidenceAvailable   EvidenceAvailability = "available"
)

// NodeInventoryRecord is the canonical read-only input consumed by the 0.30
// Sites/Nodes/Inventory view. It is deliberately observation-only: building a
// view must never enroll a node, mutate topology, or apply desired state.
type NodeInventoryRecord struct {
	NodeID            string
	Hostname          string
	SiteID            string
	ManagementZoneID  string
	Capabilities      []string
	Roles             []corecontracts.NodeRole
	CollectedAt       time.Time
	Hardware          agent.HardwareInventory
	NetworkInterfaces []agent.NetworkInterface
}

// NodeInventoryRecordFromEnrollment converts only the explicit v2 agent
// inventory contract. Legacy enrollment lacks the site, hardware and freshness
// evidence needed by the 0.30 UI and is therefore rejected instead of being
// displayed as complete inventory.
func NodeInventoryRecordFromEnrollment(request agent.EnrollmentRequest) (NodeInventoryRecord, error) {
	normalized, err := agent.NormalizeEnrollment(request)
	if err != nil {
		return NodeInventoryRecord{}, fmt.Errorf("%w: normalize enrollment: %v", ErrInvalidInfrastructureInventory, err)
	}
	if normalized.ContractVersion != agent.EnrollmentContractV2 || normalized.CollectedAt == nil || normalized.Hardware == nil {
		return NodeInventoryRecord{}, fmt.Errorf("%w: agent enrollment v2 with collected_at and hardware is required", ErrInvalidInfrastructureInventory)
	}
	return NodeInventoryRecord{
		NodeID:            normalized.NodeID,
		Hostname:          normalized.Hostname,
		SiteID:            normalized.SiteID,
		ManagementZoneID:  normalized.ManagementZoneID,
		Capabilities:      append([]string(nil), normalized.Capabilities...),
		Roles:             append([]corecontracts.NodeRole(nil), normalized.Roles...),
		CollectedAt:       normalized.CollectedAt.UTC(),
		Hardware:          *normalized.Hardware,
		NetworkInterfaces: append([]agent.NetworkInterface(nil), normalized.NetworkInterfaces...),
	}, nil
}

type InfrastructureInventoryInput struct {
	Sites       []corecontracts.Site
	Nodes       []NodeInventoryRecord
	SitesLoaded bool
	NodesLoaded bool
	Now         time.Time
	StaleAfter  time.Duration
	ExpireAfter time.Duration
}

type InfrastructureInventory struct {
	ContractVersion string             `json:"contract_version"`
	State           InventoryViewState `json:"state"`
	GeneratedAt     *time.Time         `json:"generated_at,omitempty"`
	SiteCount       int                `json:"site_count"`
	NodeCount       int                `json:"node_count"`
	Sites           []SiteInventory    `json:"sites"`
}

type SiteInventory struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ScopeID   string          `json:"scope_id"`
	NodeCount int             `json:"node_count"`
	Nodes     []NodeInventory `json:"nodes"`
}

type NodeInventory struct {
	ID               string                   `json:"id"`
	Hostname         string                   `json:"hostname"`
	SiteID           string                   `json:"site_id"`
	ManagementZoneID string                   `json:"management_zone_id,omitempty"`
	Capabilities     []string                 `json:"capabilities"`
	Roles            []corecontracts.NodeRole `json:"roles"`
	CollectedAt      time.Time                `json:"collected_at"`
	Freshness        InventoryViewState       `json:"freshness"`
	Hardware         HardwareSummary          `json:"hardware"`
	Connectivity     ConnectivitySummary      `json:"connectivity"`
	DesiredState     EvidenceAvailability     `json:"desired_state"`
	ActualState      EvidenceAvailability     `json:"actual_state"`
	VersionSkew      EvidenceAvailability     `json:"version_skew"`
}

type HardwareSummary struct {
	Architecture   string `json:"architecture,omitempty"`
	CPUModel       string `json:"cpu_model,omitempty"`
	LogicalCores   uint16 `json:"logical_cores"`
	MemoryBytes    uint64 `json:"memory_bytes"`
	StorageBytes   uint64 `json:"storage_bytes"`
	StorageDevices int    `json:"storage_devices"`
}

type ConnectivitySummary struct {
	Interfaces int `json:"interfaces"`
	Up         int `json:"up"`
	Down       int `json:"down"`
	Unknown    int `json:"unknown"`
}

// BuildInfrastructureInventory creates a deterministic, fail-closed read
// model for the 0.30 Sites/Nodes/Inventory UI. Source availability is explicit:
// an unqueried source is not represented as an empty, healthy inventory.
func BuildInfrastructureInventory(input InfrastructureInventoryInput) (InfrastructureInventory, error) {
	view := InfrastructureInventory{
		ContractVersion: InfrastructureInventoryContractVersion,
		State:           InventoryViewUnavailable,
		Sites:           []SiteInventory{},
	}
	if !input.SitesLoaded || !input.NodesLoaded {
		return view, nil
	}
	if input.Now.IsZero() {
		return InfrastructureInventory{}, fmt.Errorf("%w: current time is required", ErrInvalidInfrastructureInventory)
	}
	if input.StaleAfter < 0 || input.ExpireAfter < 0 || input.ExpireAfter < input.StaleAfter {
		return InfrastructureInventory{}, fmt.Errorf("%w: freshness thresholds must satisfy 0 <= stale_after <= expire_after", ErrInvalidInfrastructureInventory)
	}

	now := input.Now.UTC()
	view.GeneratedAt = &now
	view.State = InventoryViewCurrent

	sitesByID := make(map[string]int, len(input.Sites))
	for _, site := range input.Sites {
		if err := corecontracts.ValidateObjectIdentifier(site.ID); err != nil {
			return InfrastructureInventory{}, fmt.Errorf("%w: invalid site id %q", ErrInvalidInfrastructureInventory, site.ID)
		}
		if err := corecontracts.ValidateObjectIdentifier(site.ScopeID); err != nil {
			return InfrastructureInventory{}, fmt.Errorf("%w: invalid site scope %q", ErrInvalidInfrastructureInventory, site.ScopeID)
		}
		name := strings.TrimSpace(site.Name)
		if name == "" || name != site.Name {
			return InfrastructureInventory{}, fmt.Errorf("%w: site %q has an invalid display name", ErrInvalidInfrastructureInventory, site.ID)
		}
		if _, exists := sitesByID[site.ID]; exists {
			return InfrastructureInventory{}, fmt.Errorf("%w: duplicate site %q", ErrInvalidInfrastructureInventory, site.ID)
		}
		sitesByID[site.ID] = len(view.Sites)
		view.Sites = append(view.Sites, SiteInventory{ID: site.ID, Name: site.Name, ScopeID: site.ScopeID, Nodes: []NodeInventory{}})
	}

	seenNodes := make(map[string]struct{}, len(input.Nodes))
	for _, record := range input.Nodes {
		node, err := buildNodeInventory(record, now, input.StaleAfter, input.ExpireAfter)
		if err != nil {
			return InfrastructureInventory{}, err
		}
		if _, exists := seenNodes[node.ID]; exists {
			return InfrastructureInventory{}, fmt.Errorf("%w: duplicate node %q", ErrInvalidInfrastructureInventory, node.ID)
		}
		seenNodes[node.ID] = struct{}{}
		siteIndex, exists := sitesByID[node.SiteID]
		if !exists {
			return InfrastructureInventory{}, fmt.Errorf("%w: node %q references unknown site %q", ErrInvalidInfrastructureInventory, node.ID, node.SiteID)
		}
		view.Sites[siteIndex].Nodes = append(view.Sites[siteIndex].Nodes, node)
		view.NodeCount++
		view.State = worstInventoryState(view.State, node.Freshness)
	}

	for index := range view.Sites {
		sort.Slice(view.Sites[index].Nodes, func(i, j int) bool {
			if view.Sites[index].Nodes[i].Hostname != view.Sites[index].Nodes[j].Hostname {
				return view.Sites[index].Nodes[i].Hostname < view.Sites[index].Nodes[j].Hostname
			}
			return view.Sites[index].Nodes[i].ID < view.Sites[index].Nodes[j].ID
		})
		view.Sites[index].NodeCount = len(view.Sites[index].Nodes)
	}
	sort.Slice(view.Sites, func(i, j int) bool {
		if view.Sites[i].Name != view.Sites[j].Name {
			return view.Sites[i].Name < view.Sites[j].Name
		}
		return view.Sites[i].ID < view.Sites[j].ID
	})
	view.SiteCount = len(view.Sites)
	return view, nil
}

func buildNodeInventory(record NodeInventoryRecord, now time.Time, staleAfter, expireAfter time.Duration) (NodeInventory, error) {
	record.NodeID = strings.TrimSpace(record.NodeID)
	record.SiteID = strings.TrimSpace(record.SiteID)
	record.ManagementZoneID = strings.TrimSpace(record.ManagementZoneID)
	if err := corecontracts.ValidateObjectIdentifier(record.NodeID); err != nil {
		return NodeInventory{}, fmt.Errorf("%w: invalid node id %q", ErrInvalidInfrastructureInventory, record.NodeID)
	}
	if err := corecontracts.ValidateObjectIdentifier(record.SiteID); err != nil {
		return NodeInventory{}, fmt.Errorf("%w: node %q has invalid site id", ErrInvalidInfrastructureInventory, record.NodeID)
	}
	if record.ManagementZoneID != "" {
		if err := corecontracts.ValidateObjectIdentifier(record.ManagementZoneID); err != nil {
			return NodeInventory{}, fmt.Errorf("%w: node %q has invalid management zone id", ErrInvalidInfrastructureInventory, record.NodeID)
		}
	}
	hostname := strings.TrimSpace(record.Hostname)
	if hostname == "" || hostname != record.Hostname || len(hostname) > 253 {
		return NodeInventory{}, fmt.Errorf("%w: node %q has invalid hostname", ErrInvalidInfrastructureInventory, record.NodeID)
	}
	if record.CollectedAt.IsZero() {
		return NodeInventory{}, fmt.Errorf("%w: node %q has no collected_at evidence", ErrInvalidInfrastructureInventory, record.NodeID)
	}

	capabilities, err := canonicalStrings(record.Capabilities)
	if err != nil {
		return NodeInventory{}, fmt.Errorf("%w: node %q capabilities: %v", ErrInvalidInfrastructureInventory, record.NodeID, err)
	}
	roles, err := canonicalRoles(record.Roles)
	if err != nil {
		return NodeInventory{}, fmt.Errorf("%w: node %q roles: %v", ErrInvalidInfrastructureInventory, record.NodeID, err)
	}

	freshness := inventory.EvaluateFreshness(record.CollectedAt.UTC(), now, staleAfter, expireAfter)
	node := NodeInventory{
		ID:               record.NodeID,
		Hostname:         hostname,
		SiteID:           record.SiteID,
		ManagementZoneID: record.ManagementZoneID,
		Capabilities:     capabilities,
		Roles:            roles,
		CollectedAt:      record.CollectedAt.UTC(),
		Freshness:        mapFreshness(freshness),
		Hardware:         summarizeHardware(record.Hardware),
		Connectivity:     summarizeConnectivity(record.NetworkInterfaces),
		DesiredState:     EvidenceUnavailable,
		ActualState:      EvidenceUnavailable,
		VersionSkew:      EvidenceUnavailable,
	}
	return node, nil
}

func summarizeHardware(hardware agent.HardwareInventory) HardwareSummary {
	var storageBytes uint64
	for _, device := range hardware.Storage {
		if ^uint64(0)-storageBytes < device.CapacityBytes {
			storageBytes = ^uint64(0)
			break
		}
		storageBytes += device.CapacityBytes
	}
	return HardwareSummary{
		Architecture:   hardware.Architecture,
		CPUModel:       hardware.CPU.Model,
		LogicalCores:   hardware.CPU.LogicalCores,
		MemoryBytes:    hardware.MemoryBytes,
		StorageBytes:   storageBytes,
		StorageDevices: len(hardware.Storage),
	}
}

func summarizeConnectivity(interfaces []agent.NetworkInterface) ConnectivitySummary {
	summary := ConnectivitySummary{Interfaces: len(interfaces)}
	for _, networkInterface := range interfaces {
		switch networkInterface.OperationalState {
		case agent.LinkUp:
			summary.Up++
		case agent.LinkDown:
			summary.Down++
		default:
			summary.Unknown++
		}
	}
	return summary
}

func canonicalStrings(values []string) ([]string, error) {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return nil, errors.New("empty value")
		}
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func canonicalRoles(values []corecontracts.NodeRole) ([]corecontracts.NodeRole, error) {
	set := make(map[corecontracts.NodeRole]struct{}, len(values))
	for _, role := range values {
		role = corecontracts.NodeRole(strings.ToLower(strings.TrimSpace(string(role))))
		if !role.Valid() {
			return nil, fmt.Errorf("unsupported role %q", role)
		}
		set[role] = struct{}{}
	}
	result := make([]corecontracts.NodeRole, 0, len(set))
	for role := range set {
		result = append(result, role)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func mapFreshness(state inventory.FreshnessState) InventoryViewState {
	switch state {
	case inventory.FreshnessStale:
		return InventoryViewStale
	case inventory.FreshnessExpired:
		return InventoryViewExpired
	default:
		return InventoryViewCurrent
	}
}

func worstInventoryState(left, right InventoryViewState) InventoryViewState {
	rank := map[InventoryViewState]int{
		InventoryViewUnavailable: 4,
		InventoryViewExpired:     3,
		InventoryViewStale:       2,
		InventoryViewCurrent:     1,
	}
	if rank[right] > rank[left] {
		return right
	}
	return left
}
