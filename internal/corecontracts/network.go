package corecontracts

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

var ErrInvalidNetworkContract = errors.New("invalid distributed network contract")

const (
	maxNetworkInterfacesPerNode  = 64
	maxNetworkInterfaceAddresses = 64
)

// NetworkZoneKind is a closed, classification-only set. A zone record never
// enables routing, forwarding, NAT, firewall rules, or any host mutation.
type NetworkZoneKind string

const (
	NetworkZoneUnassigned NetworkZoneKind = "unassigned"
	NetworkZoneWAN        NetworkZoneKind = "wan"
	NetworkZoneLAN        NetworkZoneKind = "lan"
	NetworkZoneManagement NetworkZoneKind = "management"
	NetworkZoneDMZ        NetworkZoneKind = "dmz"
	NetworkZoneCluster    NetworkZoneKind = "cluster"
	NetworkZoneStorage    NetworkZoneKind = "storage"
	NetworkZoneBackup     NetworkZoneKind = "backup"
	NetworkZoneTrusted    NetworkZoneKind = "trusted"
)

func (kind NetworkZoneKind) Valid() bool {
	switch kind {
	case NetworkZoneUnassigned,
		NetworkZoneWAN,
		NetworkZoneLAN,
		NetworkZoneManagement,
		NetworkZoneDMZ,
		NetworkZoneCluster,
		NetworkZoneStorage,
		NetworkZoneBackup,
		NetworkZoneTrusted:
		return true
	default:
		return false
	}
}

// NetworkInterfaceKind is the closed inventory kind shared by distributed
// Network Interface documents.
type NetworkInterfaceKind string

const (
	NetworkInterfacePhysical NetworkInterfaceKind = "physical"
	NetworkInterfaceBond     NetworkInterfaceKind = "bond"
	NetworkInterfaceBridge   NetworkInterfaceKind = "bridge"
	NetworkInterfaceVLAN     NetworkInterfaceKind = "vlan"
	NetworkInterfaceVirtual  NetworkInterfaceKind = "virtual"
)

func (kind NetworkInterfaceKind) Valid() bool {
	switch kind {
	case NetworkInterfacePhysical,
		NetworkInterfaceBond,
		NetworkInterfaceBridge,
		NetworkInterfaceVLAN,
		NetworkInterfaceVirtual:
		return true
	default:
		return false
	}
}

// NetworkLinkState is observed inventory state, not desired host state.
type NetworkLinkState string

const (
	NetworkLinkUp      NetworkLinkState = "up"
	NetworkLinkDown    NetworkLinkState = "down"
	NetworkLinkUnknown NetworkLinkState = "unknown"
)

func (state NetworkLinkState) Valid() bool {
	switch state {
	case NetworkLinkUp, NetworkLinkDown, NetworkLinkUnknown:
		return true
	default:
		return false
	}
}

// NetworkZone is a persisted site/scope classification. It is deliberately
// separate from ManagementZone, which is an RBAC and ownership boundary.
type NetworkZone struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Kind    NetworkZoneKind `json:"kind"`
	ScopeID string          `json:"scope_id"`
	SiteID  string          `json:"site_id"`
}

// NetworkInterface is a persisted inventory/topology record. It contains no
// desired route, gateway, forwarding, NAT, firewall, or host-operation fields.
type NetworkInterface struct {
	ID                string               `json:"id"`
	NodeID            string               `json:"node_id"`
	Name              string               `json:"name"`
	Kind              NetworkInterfaceKind `json:"kind"`
	ScopeID           string               `json:"scope_id"`
	SiteID            string               `json:"site_id"`
	NetworkZoneID     string               `json:"network_zone_id"`
	MACAddress        string               `json:"mac_address,omitempty"`
	OperationalState  NetworkLinkState     `json:"operational_state"`
	Addresses         []string             `json:"addresses,omitempty"`
	MTU               uint32               `json:"mtu"`
	LinkSpeedMbps     uint64               `json:"link_speed_mbps,omitempty"`
	ParentInterfaceID string               `json:"parent_interface_id,omitempty"`
	VLANID            *uint16              `json:"vlan_id,omitempty"`
}

// ValidateNetworkContracts checks a complete distributed snapshot. A node is
// considered known at a site only when a validated RoleAssignment binds it to
// that site. This keeps the network model referentially closed while the 0.3
// Agent registry remains a separate compatibility surface.
func ValidateNetworkContracts(zones []NetworkZone, interfaces []NetworkInterface, assignments []RoleAssignment, topology Topology) error {
	zonesByID := make(map[string]NetworkZone, len(zones))
	for _, zone := range zones {
		if err := validateNetworkZone(zone, topology); err != nil {
			return err
		}
		if _, exists := zonesByID[zone.ID]; exists {
			return fmt.Errorf("%w: duplicate network zone %q", ErrInvalidNetworkContract, zone.ID)
		}
		zonesByID[zone.ID] = zone
	}

	nodesAtSite := make(map[string]map[string]struct{})
	for _, assignment := range assignments {
		if assignment.SiteID == "" {
			continue
		}
		if nodesAtSite[assignment.SiteID] == nil {
			nodesAtSite[assignment.SiteID] = make(map[string]struct{})
		}
		nodesAtSite[assignment.SiteID][assignment.TargetNodeID] = struct{}{}
	}

	interfacesByID := make(map[string]NetworkInterface, len(interfaces))
	interfaceNames := make(map[string]string, len(interfaces))
	interfaceCount := make(map[string]int)
	for _, networkInterface := range interfaces {
		if err := validateNetworkInterface(networkInterface, zonesByID, nodesAtSite, topology); err != nil {
			return err
		}
		if _, exists := interfacesByID[networkInterface.ID]; exists {
			return fmt.Errorf("%w: duplicate network interface %q", ErrInvalidNetworkContract, networkInterface.ID)
		}
		interfacesByID[networkInterface.ID] = networkInterface
		nameKey := networkInterface.NodeID + "\x00" + strings.ToLower(networkInterface.Name)
		if other, exists := interfaceNames[nameKey]; exists {
			return fmt.Errorf("%w: interfaces %q and %q repeat name %q on node %q", ErrInvalidNetworkContract, other, networkInterface.ID, networkInterface.Name, networkInterface.NodeID)
		}
		interfaceNames[nameKey] = networkInterface.ID
		interfaceCount[networkInterface.NodeID]++
		if interfaceCount[networkInterface.NodeID] > maxNetworkInterfacesPerNode {
			return fmt.Errorf("%w: node %q exceeds %d network interfaces", ErrInvalidNetworkContract, networkInterface.NodeID, maxNetworkInterfacesPerNode)
		}
	}

	parents := make(map[string]string, len(interfaces))
	vlanBindings := make(map[string]string)
	for _, networkInterface := range interfaces {
		parents[networkInterface.ID] = networkInterface.ParentInterfaceID
		if networkInterface.ParentInterfaceID == "" {
			continue
		}
		parent, exists := interfacesByID[networkInterface.ParentInterfaceID]
		if !exists || parent.ID == networkInterface.ID {
			return fmt.Errorf("%w: interface %q references invalid parent %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.ParentInterfaceID)
		}
		if parent.NodeID != networkInterface.NodeID || parent.SiteID != networkInterface.SiteID || parent.ScopeID != networkInterface.ScopeID {
			return fmt.Errorf("%w: interface %q parent must belong to the same node, site, and scope", ErrInvalidNetworkContract, networkInterface.ID)
		}
		if networkInterface.Kind == NetworkInterfaceVLAN {
			bindingKey := networkInterface.NodeID + "\x00" + networkInterface.ParentInterfaceID + fmt.Sprintf("\x00%d", *networkInterface.VLANID)
			if other, exists := vlanBindings[bindingKey]; exists {
				return fmt.Errorf("%w: VLAN interfaces %q and %q repeat parent/VLAN binding", ErrInvalidNetworkContract, other, networkInterface.ID)
			}
			vlanBindings[bindingKey] = networkInterface.ID
		}
	}
	for id := range parents {
		seen := make(map[string]struct{}, len(parents))
		for current := id; current != ""; current = parents[current] {
			if _, exists := seen[current]; exists {
				return fmt.Errorf("%w: network interface parent relationship contains a cycle", ErrInvalidNetworkContract)
			}
			seen[current] = struct{}{}
		}
	}
	return nil
}

func validateNetworkZone(zone NetworkZone, topology Topology) error {
	if err := validateIdentifier("network_zone.id", zone.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidNetworkContract, err)
	}
	if err := validateDisplayName("network_zone.name", zone.Name); err != nil {
		return fmt.Errorf("%w: zone %q: %v", ErrInvalidNetworkContract, zone.ID, err)
	}
	if !zone.Kind.Valid() {
		return fmt.Errorf("%w: zone %q has unsupported kind %q", ErrInvalidNetworkContract, zone.ID, zone.Kind)
	}
	if err := validateIdentifier("network_zone.scope_id", zone.ScopeID); err != nil {
		return fmt.Errorf("%w: zone %q: %v", ErrInvalidNetworkContract, zone.ID, err)
	}
	if err := validateIdentifier("network_zone.site_id", zone.SiteID); err != nil {
		return fmt.Errorf("%w: zone %q: %v", ErrInvalidNetworkContract, zone.ID, err)
	}
	site, exists := topology.Site(zone.SiteID)
	if !exists {
		return fmt.Errorf("%w: zone %q has unknown site %q", ErrInvalidNetworkContract, zone.ID, zone.SiteID)
	}
	if !topology.Contains(site.ScopeID, zone.ScopeID) {
		return fmt.Errorf("%w: zone %q scope %q is outside site %q", ErrInvalidNetworkContract, zone.ID, zone.ScopeID, zone.SiteID)
	}
	return nil
}

func validateNetworkInterface(networkInterface NetworkInterface, zones map[string]NetworkZone, nodesAtSite map[string]map[string]struct{}, topology Topology) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "network_interface.id", value: networkInterface.ID},
		{name: "network_interface.node_id", value: networkInterface.NodeID},
		{name: "network_interface.scope_id", value: networkInterface.ScopeID},
		{name: "network_interface.site_id", value: networkInterface.SiteID},
		{name: "network_interface.network_zone_id", value: networkInterface.NetworkZoneID},
	} {
		if err := validateIdentifier(field.name, field.value); err != nil {
			return fmt.Errorf("%w: interface %q: %v", ErrInvalidNetworkContract, networkInterface.ID, err)
		}
	}
	if err := validateDisplayName("network_interface.name", networkInterface.Name); err != nil {
		return fmt.Errorf("%w: interface %q: %v", ErrInvalidNetworkContract, networkInterface.ID, err)
	}
	if !networkInterface.Kind.Valid() {
		return fmt.Errorf("%w: interface %q has unsupported kind %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.Kind)
	}
	if !networkInterface.OperationalState.Valid() {
		return fmt.Errorf("%w: interface %q has unsupported operational_state %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.OperationalState)
	}
	site, exists := topology.Site(networkInterface.SiteID)
	if !exists {
		return fmt.Errorf("%w: interface %q has unknown site %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.SiteID)
	}
	if !topology.Contains(site.ScopeID, networkInterface.ScopeID) {
		return fmt.Errorf("%w: interface %q scope %q is outside site %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.ScopeID, networkInterface.SiteID)
	}
	zone, exists := zones[networkInterface.NetworkZoneID]
	if !exists {
		return fmt.Errorf("%w: interface %q has unknown network zone %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.NetworkZoneID)
	}
	if zone.SiteID != networkInterface.SiteID || !topology.Contains(zone.ScopeID, networkInterface.ScopeID) {
		return fmt.Errorf("%w: interface %q scope/site is outside network zone %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.NetworkZoneID)
	}
	if _, exists := nodesAtSite[networkInterface.SiteID][networkInterface.NodeID]; !exists {
		return fmt.Errorf("%w: interface %q references node %q without a role assignment at site %q", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.NodeID, networkInterface.SiteID)
	}
	if networkInterface.MTU < 68 || networkInterface.MTU > 65535 {
		return fmt.Errorf("%w: interface %q MTU must be between 68 and 65535", ErrInvalidNetworkContract, networkInterface.ID)
	}
	if networkInterface.LinkSpeedMbps > 10_000_000 {
		return fmt.Errorf("%w: interface %q link_speed_mbps exceeds the supported maximum", ErrInvalidNetworkContract, networkInterface.ID)
	}
	if networkInterface.MACAddress != "" {
		parsed, err := net.ParseMAC(networkInterface.MACAddress)
		if err != nil || len(parsed) != 6 || parsed.String() != networkInterface.MACAddress {
			return fmt.Errorf("%w: interface %q requires a canonical 48-bit mac_address", ErrInvalidNetworkContract, networkInterface.ID)
		}
	} else if networkInterface.Kind == NetworkInterfacePhysical {
		return fmt.Errorf("%w: physical interface %q requires mac_address", ErrInvalidNetworkContract, networkInterface.ID)
	}
	if len(networkInterface.Addresses) > maxNetworkInterfaceAddresses {
		return fmt.Errorf("%w: interface %q exceeds %d addresses", ErrInvalidNetworkContract, networkInterface.ID, maxNetworkInterfaceAddresses)
	}
	addresses := make(map[string]struct{}, len(networkInterface.Addresses))
	for _, address := range networkInterface.Addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil || prefix.Addr().Is4In6() || prefix.String() != address {
			return fmt.Errorf("%w: interface %q address %q must be a canonical IPv4 or IPv6 CIDR prefix", ErrInvalidNetworkContract, networkInterface.ID, address)
		}
		if _, exists := addresses[address]; exists {
			return fmt.Errorf("%w: interface %q repeats address %q", ErrInvalidNetworkContract, networkInterface.ID, address)
		}
		addresses[address] = struct{}{}
	}
	if networkInterface.Kind == NetworkInterfaceVLAN {
		if networkInterface.ParentInterfaceID == "" || networkInterface.VLANID == nil || *networkInterface.VLANID == 0 || *networkInterface.VLANID > 4094 {
			return fmt.Errorf("%w: VLAN interface %q requires parent_interface_id and vlan_id 1..4094", ErrInvalidNetworkContract, networkInterface.ID)
		}
	} else if networkInterface.VLANID != nil {
		return fmt.Errorf("%w: non-VLAN interface %q must not declare vlan_id", ErrInvalidNetworkContract, networkInterface.ID)
	}
	if networkInterface.ParentInterfaceID != "" && networkInterface.Kind != NetworkInterfaceVLAN && networkInterface.Kind != NetworkInterfaceVirtual {
		return fmt.Errorf("%w: interface %q kind %q must not declare parent_interface_id", ErrInvalidNetworkContract, networkInterface.ID, networkInterface.Kind)
	}
	return nil
}
