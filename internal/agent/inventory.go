package agent

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"control-center/internal/corecontracts"
)

const (
	maxStorageDevices      = 64
	maxNetworkInterfaces   = 64
	maxInterfaceAddresses  = 64
	maxCertificateDNSNames = 32
)

type StorageKind string

const (
	StorageHDD   StorageKind = "hdd"
	StorageSSD   StorageKind = "ssd"
	StorageNVMe  StorageKind = "nvme"
	StorageOther StorageKind = "other"
)

type CPUInventory struct {
	Model         string `json:"model"`
	Sockets       uint16 `json:"sockets"`
	PhysicalCores uint16 `json:"physical_cores"`
	LogicalCores  uint16 `json:"logical_cores"`
}

type StorageDevice struct {
	ID            string      `json:"id"`
	Kind          StorageKind `json:"kind"`
	CapacityBytes uint64      `json:"capacity_bytes"`
	Boot          bool        `json:"boot,omitempty"`
}

type HardwareInventory struct {
	MachineID    string          `json:"machine_id,omitempty"`
	ProductUUID  string          `json:"product_uuid,omitempty"`
	Manufacturer string          `json:"manufacturer,omitempty"`
	Model        string          `json:"model,omitempty"`
	SerialNumber string          `json:"serial_number,omitempty"`
	Architecture string          `json:"architecture"`
	CPU          CPUInventory    `json:"cpu"`
	MemoryBytes  uint64          `json:"memory_bytes"`
	Storage      []StorageDevice `json:"storage"`
}

type InterfaceKind = corecontracts.NetworkInterfaceKind

const (
	InterfacePhysical = corecontracts.NetworkInterfacePhysical
	InterfaceBond     = corecontracts.NetworkInterfaceBond
	InterfaceBridge   = corecontracts.NetworkInterfaceBridge
	InterfaceVLAN     = corecontracts.NetworkInterfaceVLAN
	InterfaceVirtual  = corecontracts.NetworkInterfaceVirtual
)

type LinkState = corecontracts.NetworkLinkState

const (
	LinkUp      = corecontracts.NetworkLinkUp
	LinkDown    = corecontracts.NetworkLinkDown
	LinkUnknown = corecontracts.NetworkLinkUnknown
)

type NetworkZone = corecontracts.NetworkZoneKind

const (
	ZoneUnassigned = corecontracts.NetworkZoneUnassigned
	ZoneWAN        = corecontracts.NetworkZoneWAN
	ZoneLAN        = corecontracts.NetworkZoneLAN
	ZoneManagement = corecontracts.NetworkZoneManagement
	ZoneDMZ        = corecontracts.NetworkZoneDMZ
	ZoneCluster    = corecontracts.NetworkZoneCluster
	ZoneStorage    = corecontracts.NetworkZoneStorage
	ZoneBackup     = corecontracts.NetworkZoneBackup
	ZoneTrusted    = corecontracts.NetworkZoneTrusted
)

type NetworkInterface struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Kind              InterfaceKind `json:"kind"`
	MACAddress        string        `json:"mac_address,omitempty"`
	OperationalState  LinkState     `json:"operational_state"`
	Zone              NetworkZone   `json:"zone"`
	Addresses         []string      `json:"addresses,omitempty"`
	MTU               uint32        `json:"mtu"`
	LinkSpeedMbps     uint64        `json:"link_speed_mbps,omitempty"`
	ParentInterfaceID string        `json:"parent_interface_id,omitempty"`
	VLANID            *uint16       `json:"vlan_id,omitempty"`
}

type PublicKeyAlgorithm string

const (
	PublicKeyRSA     PublicKeyAlgorithm = "rsa"
	PublicKeyECDSA   PublicKeyAlgorithm = "ecdsa"
	PublicKeyEd25519 PublicKeyAlgorithm = "ed25519"
)

type CertificateMetadata struct {
	Subject            string             `json:"subject"`
	Issuer             string             `json:"issuer"`
	SerialNumber       string             `json:"serial_number"`
	FingerprintSHA256  string             `json:"fingerprint_sha256"`
	NotBefore          time.Time          `json:"not_before"`
	NotAfter           time.Time          `json:"not_after"`
	PublicKeyAlgorithm PublicKeyAlgorithm `json:"public_key_algorithm"`
	DNSNames           []string           `json:"dns_names,omitempty"`
}

type IdentityMetadata struct {
	AgentID                    string              `json:"agent_id"`
	InstallationID             string              `json:"installation_id"`
	TrustedCAFingerprintSHA256 string              `json:"trusted_ca_fingerprint_sha256"`
	Certificate                CertificateMetadata `json:"certificate"`
}

var hexadecimalPattern = regexp.MustCompile(`^[0-9a-f]+$`)

func normalizeHardware(hardware HardwareInventory) (HardwareInventory, error) {
	hardware.MachineID = strings.TrimSpace(hardware.MachineID)
	hardware.ProductUUID = strings.ToLower(strings.TrimSpace(hardware.ProductUUID))
	hardware.Manufacturer = strings.TrimSpace(hardware.Manufacturer)
	hardware.Model = strings.TrimSpace(hardware.Model)
	hardware.SerialNumber = strings.TrimSpace(hardware.SerialNumber)
	hardware.Architecture = strings.ToLower(strings.TrimSpace(hardware.Architecture))
	if hardware.MachineID == "" && hardware.ProductUUID == "" && hardware.SerialNumber == "" {
		return HardwareInventory{}, invalidEnrollment("hardware requires machine_id, product_uuid, or serial_number")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "hardware.machine_id", value: hardware.MachineID},
		{name: "hardware.product_uuid", value: hardware.ProductUUID},
		{name: "hardware.manufacturer", value: hardware.Manufacturer},
		{name: "hardware.model", value: hardware.Model},
		{name: "hardware.serial_number", value: hardware.SerialNumber},
	} {
		if field.value != "" {
			if err := validateBoundedText(field.name, field.value, 256); err != nil {
				return HardwareInventory{}, err
			}
		}
	}
	switch hardware.Architecture {
	case "amd64", "arm64":
	default:
		return HardwareInventory{}, invalidEnrollment("unsupported hardware architecture %q", hardware.Architecture)
	}

	hardware.CPU.Model = strings.TrimSpace(hardware.CPU.Model)
	if err := validateBoundedText("hardware.cpu.model", hardware.CPU.Model, 256); err != nil {
		return HardwareInventory{}, err
	}
	if hardware.CPU.Sockets == 0 || hardware.CPU.PhysicalCores == 0 || hardware.CPU.LogicalCores == 0 {
		return HardwareInventory{}, invalidEnrollment("hardware CPU socket/core counts must be positive")
	}
	if hardware.CPU.Sockets > hardware.CPU.PhysicalCores || hardware.CPU.PhysicalCores > hardware.CPU.LogicalCores {
		return HardwareInventory{}, invalidEnrollment("hardware CPU topology is inconsistent")
	}
	if hardware.MemoryBytes == 0 {
		return HardwareInventory{}, invalidEnrollment("hardware.memory_bytes must be positive")
	}
	if len(hardware.Storage) == 0 || len(hardware.Storage) > maxStorageDevices {
		return HardwareInventory{}, invalidEnrollment("hardware.storage must contain between 1 and %d devices", maxStorageDevices)
	}

	storage := make([]StorageDevice, 0, len(hardware.Storage))
	seen := make(map[string]struct{}, len(hardware.Storage))
	for _, device := range hardware.Storage {
		device.ID = strings.TrimSpace(device.ID)
		device.Kind = StorageKind(strings.ToLower(strings.TrimSpace(string(device.Kind))))
		if err := validateBoundedText("hardware.storage.id", device.ID, 128); err != nil {
			return HardwareInventory{}, err
		}
		switch device.Kind {
		case StorageHDD, StorageSSD, StorageNVMe, StorageOther:
		default:
			return HardwareInventory{}, invalidEnrollment("unsupported storage kind %q", device.Kind)
		}
		if device.CapacityBytes == 0 {
			return HardwareInventory{}, invalidEnrollment("storage %q capacity_bytes must be positive", device.ID)
		}
		key := strings.ToLower(device.ID)
		if _, exists := seen[key]; exists {
			return HardwareInventory{}, invalidEnrollment("duplicate storage id %q", device.ID)
		}
		seen[key] = struct{}{}
		storage = append(storage, device)
	}
	sort.Slice(storage, func(i, j int) bool { return strings.ToLower(storage[i].ID) < strings.ToLower(storage[j].ID) })
	hardware.Storage = storage
	return hardware, nil
}

func normalizeNetworkInterfaces(interfaces []NetworkInterface) ([]NetworkInterface, error) {
	if len(interfaces) == 0 || len(interfaces) > maxNetworkInterfaces {
		return nil, invalidEnrollment("network_interfaces must contain between 1 and %d interfaces", maxNetworkInterfaces)
	}
	result := make([]NetworkInterface, 0, len(interfaces))
	ids := make(map[string]string, len(interfaces))
	for _, networkInterface := range interfaces {
		networkInterface.ID = strings.TrimSpace(networkInterface.ID)
		networkInterface.Name = strings.TrimSpace(networkInterface.Name)
		networkInterface.Kind = InterfaceKind(strings.ToLower(strings.TrimSpace(string(networkInterface.Kind))))
		networkInterface.OperationalState = LinkState(strings.ToLower(strings.TrimSpace(string(networkInterface.OperationalState))))
		networkInterface.Zone = NetworkZone(strings.ToLower(strings.TrimSpace(string(networkInterface.Zone))))
		networkInterface.ParentInterfaceID = strings.TrimSpace(networkInterface.ParentInterfaceID)
		if err := validateIdentifier("network_interfaces.id", networkInterface.ID, 128); err != nil {
			return nil, err
		}
		if err := validateBoundedText("network_interfaces.name", networkInterface.Name, 128); err != nil {
			return nil, err
		}
		switch networkInterface.Kind {
		case InterfacePhysical, InterfaceBond, InterfaceBridge, InterfaceVLAN, InterfaceVirtual:
		default:
			return nil, invalidEnrollment("interface %q has unsupported kind %q", networkInterface.ID, networkInterface.Kind)
		}
		switch networkInterface.OperationalState {
		case LinkUp, LinkDown, LinkUnknown:
		default:
			return nil, invalidEnrollment("interface %q has unsupported operational_state %q", networkInterface.ID, networkInterface.OperationalState)
		}
		switch networkInterface.Zone {
		case ZoneUnassigned, ZoneWAN, ZoneLAN, ZoneManagement, ZoneDMZ, ZoneCluster, ZoneStorage, ZoneBackup, ZoneTrusted:
		default:
			return nil, invalidEnrollment("interface %q has unsupported zone %q", networkInterface.ID, networkInterface.Zone)
		}
		if networkInterface.MTU < 68 || networkInterface.MTU > 65535 {
			return nil, invalidEnrollment("interface %q mtu must be between 68 and 65535", networkInterface.ID)
		}
		if networkInterface.LinkSpeedMbps > 10_000_000 {
			return nil, invalidEnrollment("interface %q link_speed_mbps exceeds the supported maximum", networkInterface.ID)
		}
		if networkInterface.MACAddress != "" {
			rawMACAddress := strings.TrimSpace(networkInterface.MACAddress)
			if len(rawMACAddress) > 32 {
				return nil, invalidEnrollment("interface %q has invalid 48-bit MAC address", networkInterface.ID)
			}
			hardwareAddress, err := net.ParseMAC(rawMACAddress)
			if err != nil || len(hardwareAddress) != 6 {
				return nil, invalidEnrollment("interface %q has invalid 48-bit MAC address", networkInterface.ID)
			}
			networkInterface.MACAddress = hardwareAddress.String()
		} else if networkInterface.Kind == InterfacePhysical {
			return nil, invalidEnrollment("physical interface %q requires mac_address", networkInterface.ID)
		}
		addresses, err := normalizeInterfaceAddresses(networkInterface.ID, networkInterface.Addresses)
		if err != nil {
			return nil, err
		}
		networkInterface.Addresses = addresses
		if networkInterface.Kind == InterfaceVLAN {
			if networkInterface.ParentInterfaceID == "" || networkInterface.VLANID == nil || *networkInterface.VLANID == 0 || *networkInterface.VLANID > 4094 {
				return nil, invalidEnrollment("VLAN interface %q requires parent_interface_id and vlan_id 1..4094", networkInterface.ID)
			}
		} else if networkInterface.VLANID != nil {
			return nil, invalidEnrollment("non-VLAN interface %q must not declare vlan_id", networkInterface.ID)
		}
		if networkInterface.ParentInterfaceID != "" && networkInterface.Kind != InterfaceVLAN && networkInterface.Kind != InterfaceVirtual {
			return nil, invalidEnrollment("interface %q kind %q must not declare parent_interface_id", networkInterface.ID, networkInterface.Kind)
		}
		key := strings.ToLower(networkInterface.ID)
		if _, exists := ids[key]; exists {
			return nil, invalidEnrollment("duplicate interface id %q", networkInterface.ID)
		}
		ids[key] = networkInterface.ID
		result = append(result, networkInterface)
	}

	for index := range result {
		parent := strings.ToLower(result[index].ParentInterfaceID)
		if parent == "" {
			continue
		}
		canonical, exists := ids[parent]
		if !exists || strings.EqualFold(canonical, result[index].ID) {
			return nil, invalidEnrollment("interface %q references an invalid parent %q", result[index].ID, result[index].ParentInterfaceID)
		}
		result[index].ParentInterfaceID = canonical
	}
	if err := validateInterfaceParentGraph(result); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].ID) < strings.ToLower(result[j].ID) })
	return result, nil
}

func normalizeInterfaceAddresses(interfaceID string, addresses []string) ([]string, error) {
	if len(addresses) > maxInterfaceAddresses {
		return nil, invalidEnrollment("interface %q addresses exceeds maximum of %d", interfaceID, maxInterfaceAddresses)
	}
	result := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, rawAddress := range addresses {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(rawAddress))
		if err != nil || prefix.Addr().Is4In6() {
			return nil, invalidEnrollment("interface %q address %q must be an IPv4 or IPv6 CIDR prefix", interfaceID, rawAddress)
		}
		address := prefix.String()
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	sort.Strings(result)
	return result, nil
}

func validateInterfaceParentGraph(interfaces []NetworkInterface) error {
	parents := make(map[string]string, len(interfaces))
	for _, networkInterface := range interfaces {
		parents[strings.ToLower(networkInterface.ID)] = strings.ToLower(networkInterface.ParentInterfaceID)
	}
	for id := range parents {
		seen := make(map[string]struct{}, len(parents))
		for current := id; current != ""; current = parents[current] {
			if _, exists := seen[current]; exists {
				return invalidEnrollment("network interface parent relationship contains a cycle")
			}
			seen[current] = struct{}{}
		}
	}
	return nil
}

func normalizeIdentity(identity IdentityMetadata) (IdentityMetadata, error) {
	identity.AgentID = strings.TrimSpace(identity.AgentID)
	identity.InstallationID = strings.TrimSpace(identity.InstallationID)
	if err := validateIdentifier("identity.agent_id", identity.AgentID, 128); err != nil {
		return IdentityMetadata{}, err
	}
	if err := validateIdentifier("identity.installation_id", identity.InstallationID, 128); err != nil {
		return IdentityMetadata{}, err
	}
	fingerprint, err := normalizeSHA256Fingerprint(identity.TrustedCAFingerprintSHA256)
	if err != nil {
		return IdentityMetadata{}, invalidEnrollment("identity.trusted_ca_fingerprint_sha256 is invalid")
	}
	identity.TrustedCAFingerprintSHA256 = fingerprint

	certificate := identity.Certificate
	certificate.Subject = strings.TrimSpace(certificate.Subject)
	certificate.Issuer = strings.TrimSpace(certificate.Issuer)
	if err := validateBoundedText("identity.certificate.subject", certificate.Subject, 512); err != nil {
		return IdentityMetadata{}, err
	}
	if err := validateBoundedText("identity.certificate.issuer", certificate.Issuer, 512); err != nil {
		return IdentityMetadata{}, err
	}
	rawSerial := strings.TrimSpace(certificate.SerialNumber)
	if len(rawSerial) > 128 {
		return IdentityMetadata{}, invalidEnrollment("identity.certificate.serial_number is invalid")
	}
	serial := strings.ToLower(strings.ReplaceAll(rawSerial, ":", ""))
	if len(serial) == 0 || len(serial) > 128 || !hexadecimalPattern.MatchString(serial) {
		return IdentityMetadata{}, invalidEnrollment("identity.certificate.serial_number is invalid")
	}
	certificate.SerialNumber = serial
	fingerprint, err = normalizeSHA256Fingerprint(certificate.FingerprintSHA256)
	if err != nil {
		return IdentityMetadata{}, invalidEnrollment("identity.certificate.fingerprint_sha256 is invalid")
	}
	certificate.FingerprintSHA256 = fingerprint
	if certificate.NotBefore.IsZero() || certificate.NotAfter.IsZero() || !certificate.NotBefore.Before(certificate.NotAfter) {
		return IdentityMetadata{}, invalidEnrollment("identity certificate validity window is invalid")
	}
	certificate.NotBefore = certificate.NotBefore.UTC()
	certificate.NotAfter = certificate.NotAfter.UTC()
	certificate.PublicKeyAlgorithm = PublicKeyAlgorithm(strings.ToLower(strings.TrimSpace(string(certificate.PublicKeyAlgorithm))))
	switch certificate.PublicKeyAlgorithm {
	case PublicKeyRSA, PublicKeyECDSA, PublicKeyEd25519:
	default:
		return IdentityMetadata{}, invalidEnrollment("identity certificate public_key_algorithm is unsupported")
	}
	if len(certificate.DNSNames) > maxCertificateDNSNames {
		return IdentityMetadata{}, invalidEnrollment("identity certificate dns_names exceeds maximum of %d", maxCertificateDNSNames)
	}
	dnsNames := make([]string, 0, len(certificate.DNSNames))
	seen := make(map[string]struct{}, len(certificate.DNSNames))
	for _, value := range certificate.DNSNames {
		name := strings.ToLower(strings.TrimSpace(value))
		if err := validateHostname(name); err != nil {
			return IdentityMetadata{}, invalidEnrollment("identity certificate contains invalid DNS name %q", value)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		dnsNames = append(dnsNames, name)
	}
	sort.Strings(dnsNames)
	certificate.DNSNames = dnsNames
	identity.Certificate = certificate
	return identity, nil
}

func normalizeSHA256Fingerprint(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 103 {
		return "", fmt.Errorf("invalid SHA-256 fingerprint")
	}
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.ReplaceAll(value, ":", "")
	if len(value) != 64 || !hexadecimalPattern.MatchString(value) {
		return "", fmt.Errorf("invalid SHA-256 fingerprint")
	}
	return "sha256:" + value, nil
}
