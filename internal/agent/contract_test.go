package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

func validV2Enrollment() EnrollmentRequest {
	collectedAt := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return EnrollmentRequest{
		ContractVersion: EnrollmentContractV2,
		NodeID:          "node-001",
		Hostname:        "Node-001.Example.Test",
		Capabilities:    []string{"Monitoring", "inventory", "inventory"},
		Roles: []corecontracts.NodeRole{
			corecontracts.RoleWorkerNode,
			corecontracts.RoleManagementNode,
			corecontracts.RoleAgent,
			corecontracts.RoleWorkerNode,
		},
		SiteID:           "site-a",
		ManagementZoneID: "zone-primary",
		CollectedAt:      &collectedAt,
		Hardware: &HardwareInventory{
			MachineID:    "machine-001",
			Manufacturer: "Example Systems",
			Model:        "CC-Node",
			Architecture: "AMD64",
			CPU: CPUInventory{
				Model:         "Example CPU",
				Sockets:       1,
				PhysicalCores: 8,
				LogicalCores:  16,
			},
			MemoryBytes: 32 << 30,
			Storage: []StorageDevice{
				{ID: "disk-b", Kind: StorageHDD, CapacityBytes: 2 << 40},
				{ID: "disk-a", Kind: StorageNVMe, CapacityBytes: 1 << 40, Boot: true},
			},
		},
		NetworkInterfaces: []NetworkInterface{
			{
				ID: "nic-wan", Name: "eth1", Kind: InterfacePhysical,
				MACAddress: "02-00-00-00-00-02", OperationalState: LinkUp,
				Zone: ZoneWAN, Addresses: []string{"198.51.100.10/24"}, MTU: 1500, LinkSpeedMbps: 1000,
			},
			{
				ID: "nic-management", Name: "eth0", Kind: InterfacePhysical,
				MACAddress: "02:00:00:00:00:01", OperationalState: LinkUp,
				Zone: ZoneManagement, Addresses: []string{"10.0.0.10/24", "10.0.0.10/24"}, MTU: 1500, LinkSpeedMbps: 1000,
			},
		},
		Identity: &IdentityMetadata{
			AgentID:                    "agent-001",
			InstallationID:             "install-001",
			TrustedCAFingerprintSHA256: strings.Repeat("ab:", 31) + "ab",
			Certificate: CertificateMetadata{
				Subject:            "CN=node-001",
				Issuer:             "CN=Control Center Agent CA",
				SerialNumber:       "AA:01",
				FingerprintSHA256:  strings.Repeat("cd", 32),
				NotBefore:          collectedAt.Add(-time.Hour),
				NotAfter:           collectedAt.Add(24 * time.Hour),
				PublicKeyAlgorithm: PublicKeyEd25519,
				DNSNames:           []string{"NODE-001.EXAMPLE.TEST", "node-001.example.test"},
			},
		},
		CapacityObservations: []CapacityObservation{
			{Metric: MetricStorageUsed, TargetID: "disk-a", Value: 100 << 30, Unit: UnitBytes, ObservedAt: collectedAt.Add(-time.Minute), Evidence: EvidenceMeasured},
			{Metric: MetricCPUUtilization, TargetID: "NODE-001", Value: 25, Unit: UnitPercent, ObservedAt: collectedAt.Add(-time.Minute), Evidence: EvidenceMeasured},
			{Metric: MetricNetworkThroughput, TargetID: "NIC-MANAGEMENT", Value: 50_000_000, Unit: UnitBitsPerSecond, ObservedAt: collectedAt.Add(-time.Minute), Evidence: EvidenceMeasured},
			{Metric: MetricMemoryUsed, TargetID: "node-001", Value: 8 << 30, Unit: UnitBytes, ObservedAt: collectedAt.Add(-time.Minute), Evidence: EvidenceMeasured},
		},
	}
}

func TestNormalizeEnrollmentV2CanonicalizesInventoryAndIsReady(t *testing.T) {
	result, err := NormalizeEnrollmentContract(validV2Enrollment())
	if err != nil {
		t.Fatalf("NormalizeEnrollmentContract() error = %v", err)
	}
	if !result.Preconditions.Ready {
		t.Fatalf("preconditions = %#v", result.Preconditions)
	}
	if result.Effects.NetworkMutation || result.Effects.PersistsEnrollment {
		t.Fatalf("normalization unexpectedly has effects: %#v", result.Effects)
	}
	if result.Hostname != "node-001.example.test" {
		t.Fatalf("Hostname = %q", result.Hostname)
	}
	if len(result.Roles) != 3 || result.Roles[0] != corecontracts.RoleAgent || result.Roles[1] != corecontracts.RoleManagementNode || result.Roles[2] != corecontracts.RoleWorkerNode {
		t.Fatalf("Roles = %#v", result.Roles)
	}
	if result.Hardware.Storage[0].ID != "disk-a" {
		t.Fatalf("Storage = %#v", result.Hardware.Storage)
	}
	if result.NetworkInterfaces[0].ID != "nic-management" || result.NetworkInterfaces[0].MACAddress != "02:00:00:00:00:01" {
		t.Fatalf("NetworkInterfaces = %#v", result.NetworkInterfaces)
	}
	if len(result.NetworkInterfaces[0].Addresses) != 1 {
		t.Fatalf("Addresses = %#v", result.NetworkInterfaces[0].Addresses)
	}
	if got := result.Identity.TrustedCAFingerprintSHA256; got != "sha256:"+strings.Repeat("ab", 32) {
		t.Fatalf("CA fingerprint = %q", got)
	}
	if got := result.Identity.Certificate.SerialNumber; got != "aa01" {
		t.Fatalf("certificate serial = %q", got)
	}
	if len(result.Identity.Certificate.DNSNames) != 1 || result.Identity.Certificate.DNSNames[0] != "node-001.example.test" {
		t.Fatalf("certificate DNS names = %#v", result.Identity.Certificate.DNSNames)
	}
}

func TestNormalizeEnrollmentV2RequiresExplicitVersionForExtendedFields(t *testing.T) {
	request := validV2Enrollment()
	request.ContractVersion = ""
	_, err := NormalizeEnrollment(request)
	if !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("error = %v, want ErrInvalidEnrollment", err)
	}
}

func TestNormalizeEnrollmentV2RejectsUnknownRoleAndZone(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EnrollmentRequest)
	}{
		{name: "role", mutate: func(request *EnrollmentRequest) { request.Roles = []corecontracts.NodeRole{"regional-master"} }},
		{name: "network zone", mutate: func(request *EnrollmentRequest) { request.NetworkInterfaces[0].Zone = "internet" }},
		{name: "management zone id", mutate: func(request *EnrollmentRequest) { request.ManagementZoneID = "../../global" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validV2Enrollment()
			test.mutate(&request)
			_, err := NormalizeEnrollment(request)
			if !errors.Is(err, ErrInvalidEnrollment) {
				t.Fatalf("error = %v, want ErrInvalidEnrollment", err)
			}
		})
	}
}

func TestNormalizeEnrollmentV2RejectsUnsafeInventoryRelationships(t *testing.T) {
	vlanID := uint16(100)
	tests := []struct {
		name   string
		mutate func(*EnrollmentRequest)
	}{
		{name: "invalid cidr", mutate: func(request *EnrollmentRequest) { request.NetworkInterfaces[0].Addresses = []string{"10.0.0.1"} }},
		{name: "unknown vlan parent", mutate: func(request *EnrollmentRequest) {
			request.NetworkInterfaces = append(request.NetworkInterfaces, NetworkInterface{
				ID: "vlan100", Name: "eth9.100", Kind: InterfaceVLAN, OperationalState: LinkUp,
				Zone: ZoneLAN, Addresses: []string{"10.100.0.1/24"}, MTU: 1500,
				ParentInterfaceID: "missing", VLANID: &vlanID,
			})
		}},
		{name: "unknown capacity target", mutate: func(request *EnrollmentRequest) { request.CapacityObservations[0].TargetID = "missing-disk" }},
		{name: "memory exceeds hardware", mutate: func(request *EnrollmentRequest) { request.CapacityObservations[3].Value = 64 << 30 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validV2Enrollment()
			test.mutate(&request)
			_, err := NormalizeEnrollment(request)
			if !errors.Is(err, ErrInvalidEnrollment) {
				t.Fatalf("error = %v, want ErrInvalidEnrollment", err)
			}
		})
	}
}

func TestEnrollmentPreconditionsFailClosedWithoutManagementOrCapacityEvidence(t *testing.T) {
	request := validV2Enrollment()
	request.NetworkInterfaces[1].Zone = ZoneWAN
	request.CapacityObservations[0].Evidence = EvidenceEstimated
	result, err := NormalizeEnrollmentContract(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Preconditions.Ready {
		t.Fatalf("preconditions unexpectedly ready: %#v", result.Preconditions)
	}
	checks := make(map[string]bool, len(result.Preconditions.Checks))
	for _, check := range result.Preconditions.Checks {
		checks[check.Code] = check.Satisfied
	}
	if checks["network.management-path.observed"] || checks["capacity.required-metrics.measured"] || checks["capacity.observations.measured"] {
		t.Fatalf("fail-closed checks = %#v", checks)
	}
	if !checks["effects.network-mutation.disabled"] {
		t.Fatalf("network mutation invariant missing: %#v", checks)
	}
}

func TestDataRoleRequiresMeasuredDatabaseCapacityEvidence(t *testing.T) {
	request := validV2Enrollment()
	request.Roles = append(request.Roles, corecontracts.RoleDataNode)
	result, err := NormalizeEnrollmentContract(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Preconditions.Ready {
		t.Fatal("data role became ready without database latency evidence")
	}
	collectedAt := *request.CollectedAt
	request.CapacityObservations = append(request.CapacityObservations, CapacityObservation{
		Metric: MetricDatabaseLatency, TargetID: "control-db", Value: 2.5,
		Unit: UnitMilliseconds, ObservedAt: collectedAt.Add(-time.Minute), Evidence: EvidenceMeasured,
	})
	result, err = NormalizeEnrollmentContract(request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Preconditions.Ready {
		t.Fatalf("data role preconditions = %#v", result.Preconditions)
	}
}

func TestNormalizeEnrollmentV2EnforcesCollectionBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EnrollmentRequest)
	}{
		{name: "roles", mutate: func(request *EnrollmentRequest) {
			request.Roles = make([]corecontracts.NodeRole, maxRoles+1)
			for index := range request.Roles {
				request.Roles[index] = corecontracts.RoleWorkerNode
			}
		}},
		{name: "interfaces", mutate: func(request *EnrollmentRequest) {
			request.NetworkInterfaces = make([]NetworkInterface, maxNetworkInterfaces+1)
		}},
		{name: "capacity observations", mutate: func(request *EnrollmentRequest) {
			request.CapacityObservations = make([]CapacityObservation, maxCapacityObservations+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validV2Enrollment()
			test.mutate(&request)
			_, err := NormalizeEnrollment(request)
			if !errors.Is(err, ErrInvalidEnrollment) {
				t.Fatalf("error = %v, want ErrInvalidEnrollment", err)
			}
		})
	}
}

func TestEvaluateEnrollmentPreconditionsRejectsUnvalidatedInput(t *testing.T) {
	request := validV2Enrollment()
	request.Roles = []corecontracts.NodeRole{"unknown-role"}
	preconditions := EvaluateEnrollmentPreconditions(request)
	if preconditions.Ready || len(preconditions.Checks) != 2 || preconditions.Checks[0].Code != "contract.valid" || preconditions.Checks[0].Satisfied {
		t.Fatalf("preconditions = %#v", preconditions)
	}
	if !preconditions.Checks[1].Satisfied || preconditions.Checks[1].Code != "effects.network-mutation.disabled" {
		t.Fatalf("network mutation invariant = %#v", preconditions.Checks)
	}
}

func TestLegacyEnrollmentRemainsCompatibleButIsNotAdmissionReady(t *testing.T) {
	result, err := NormalizeEnrollmentContract(EnrollmentRequest{
		NodeID: " node-legacy ", Hostname: " legacy-host ", Capabilities: []string{"PXE", "pxe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeID != "node-legacy" || len(result.Capabilities) != 1 || result.Capabilities[0] != "pxe" {
		t.Fatalf("legacy normalization = %#v", result.EnrollmentRequest)
	}
	if result.Preconditions.Ready {
		t.Fatal("legacy normalization must not be treated as v2 admission readiness")
	}
}
