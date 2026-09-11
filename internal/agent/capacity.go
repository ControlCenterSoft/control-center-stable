package agent

import (
	"math"
	"sort"
	"strings"
	"time"

	"control-center/internal/corecontracts"
)

const (
	maxCapacityObservations = 256
	capacityFreshnessWindow = 15 * time.Minute
)

type CapacityMetric string

const (
	MetricCPUUtilization       CapacityMetric = "cpu.utilization"
	MetricMemoryUsed           CapacityMetric = "memory.used"
	MetricStorageUsed          CapacityMetric = "storage.used"
	MetricStorageIOPS          CapacityMetric = "storage.iops"
	MetricStorageLatency       CapacityMetric = "storage.latency"
	MetricStorageQueueDepth    CapacityMetric = "storage.queue-depth"
	MetricNetworkThroughput    CapacityMetric = "network.throughput"
	MetricNetworkLatency       CapacityMetric = "network.latency"
	MetricNetworkPacketLoss    CapacityMetric = "network.packet-loss"
	MetricDatabaseLatency      CapacityMetric = "database.latency"
	MetricDatabaseTransactions CapacityMetric = "database.transactions"
	MetricServiceQueueDepth    CapacityMetric = "service.queue-depth"
)

type CapacityUnit string

const (
	UnitPercent               CapacityUnit = "percent"
	UnitBytes                 CapacityUnit = "bytes"
	UnitOperationsPerSecond   CapacityUnit = "operations-per-second"
	UnitMilliseconds          CapacityUnit = "milliseconds"
	UnitCount                 CapacityUnit = "count"
	UnitBitsPerSecond         CapacityUnit = "bits-per-second"
	UnitTransactionsPerSecond CapacityUnit = "transactions-per-second"
)

type ObservationEvidence string

const (
	EvidenceMeasured  ObservationEvidence = "measured"
	EvidenceEstimated ObservationEvidence = "estimated"
	EvidenceBenchmark ObservationEvidence = "benchmark"
)

type CapacityObservation struct {
	Metric     CapacityMetric      `json:"metric"`
	TargetID   string              `json:"target_id"`
	Value      float64             `json:"value"`
	Unit       CapacityUnit        `json:"unit"`
	ObservedAt time.Time           `json:"observed_at"`
	Evidence   ObservationEvidence `json:"evidence"`
}

type EnrollmentPrecondition struct {
	Code      string `json:"code"`
	Satisfied bool   `json:"satisfied"`
	Message   string `json:"message,omitempty"`
}

type EnrollmentPreconditions struct {
	Ready  bool                     `json:"ready"`
	Checks []EnrollmentPrecondition `json:"checks"`
}

type CapacityTargetKind string

const (
	CapacityTargetNode     CapacityTargetKind = "node"
	CapacityTargetStorage  CapacityTargetKind = "storage"
	CapacityTargetNetwork  CapacityTargetKind = "network-interface"
	CapacityTargetDatabase CapacityTargetKind = "database"
	CapacityTargetService  CapacityTargetKind = "service"
)

// CapacityMetricDefinition is the single source of truth shared by Agent
// observations and generic Capacity contracts. Maximum is an input safety
// bound, not a product capacity claim.
type CapacityMetricDefinition struct {
	Unit       CapacityUnit
	TargetKind CapacityTargetKind
	Maximum    float64
}

var capacityMetricDefinitions = map[CapacityMetric]CapacityMetricDefinition{
	MetricCPUUtilization:       {Unit: UnitPercent, TargetKind: CapacityTargetNode, Maximum: 100},
	MetricMemoryUsed:           {Unit: UnitBytes, TargetKind: CapacityTargetNode, Maximum: 1e21},
	MetricStorageUsed:          {Unit: UnitBytes, TargetKind: CapacityTargetStorage, Maximum: 1e21},
	MetricStorageIOPS:          {Unit: UnitOperationsPerSecond, TargetKind: CapacityTargetStorage, Maximum: 1e21},
	MetricStorageLatency:       {Unit: UnitMilliseconds, TargetKind: CapacityTargetStorage, Maximum: 1e21},
	MetricStorageQueueDepth:    {Unit: UnitCount, TargetKind: CapacityTargetStorage, Maximum: 1e21},
	MetricNetworkThroughput:    {Unit: UnitBitsPerSecond, TargetKind: CapacityTargetNetwork, Maximum: 1e21},
	MetricNetworkLatency:       {Unit: UnitMilliseconds, TargetKind: CapacityTargetNetwork, Maximum: 1e21},
	MetricNetworkPacketLoss:    {Unit: UnitPercent, TargetKind: CapacityTargetNetwork, Maximum: 100},
	MetricDatabaseLatency:      {Unit: UnitMilliseconds, TargetKind: CapacityTargetDatabase, Maximum: 1e21},
	MetricDatabaseTransactions: {Unit: UnitTransactionsPerSecond, TargetKind: CapacityTargetDatabase, Maximum: 1e21},
	MetricServiceQueueDepth:    {Unit: UnitCount, TargetKind: CapacityTargetService, Maximum: 1e21},
}

// DefinitionForCapacityMetric returns the canonical semantics for a metric.
func DefinitionForCapacityMetric(metric CapacityMetric) (CapacityMetricDefinition, bool) {
	definition, exists := capacityMetricDefinitions[metric]
	return definition, exists
}

// ValidCapacityMetricValue applies the canonical finite/non-negative safety
// bound. More specific inventory checks remain the caller's responsibility.
func ValidCapacityMetricValue(metric CapacityMetric, value float64) bool {
	definition, exists := DefinitionForCapacityMetric(metric)
	return exists && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= definition.Maximum
}

func normalizeCapacityObservations(
	observations []CapacityObservation,
	nodeID string,
	hardware HardwareInventory,
	interfaces []NetworkInterface,
	collectedAt time.Time,
) ([]CapacityObservation, error) {
	if len(observations) > maxCapacityObservations {
		return nil, invalidEnrollment("capacity_observations exceeds maximum of %d", maxCapacityObservations)
	}
	storage := make(map[string]StorageDevice, len(hardware.Storage))
	for _, device := range hardware.Storage {
		storage[strings.ToLower(device.ID)] = device
	}
	network := make(map[string]NetworkInterface, len(interfaces))
	for _, networkInterface := range interfaces {
		network[strings.ToLower(networkInterface.ID)] = networkInterface
	}

	result := make([]CapacityObservation, 0, len(observations))
	seen := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		observation.Metric = CapacityMetric(strings.ToLower(strings.TrimSpace(string(observation.Metric))))
		observation.Unit = CapacityUnit(strings.ToLower(strings.TrimSpace(string(observation.Unit))))
		observation.Evidence = ObservationEvidence(strings.ToLower(strings.TrimSpace(string(observation.Evidence))))
		observation.TargetID = strings.TrimSpace(observation.TargetID)
		definition, exists := DefinitionForCapacityMetric(observation.Metric)
		if !exists || observation.Unit != definition.Unit {
			return nil, invalidEnrollment("capacity metric %q has unsupported unit %q", observation.Metric, observation.Unit)
		}
		switch observation.Evidence {
		case EvidenceMeasured, EvidenceEstimated, EvidenceBenchmark:
		default:
			return nil, invalidEnrollment("capacity metric %q has unsupported evidence %q", observation.Metric, observation.Evidence)
		}
		if !ValidCapacityMetricValue(observation.Metric, observation.Value) {
			return nil, invalidEnrollment("capacity metric %q value is outside supported bounds", observation.Metric)
		}
		if observation.ObservedAt.IsZero() || observation.ObservedAt.After(collectedAt.Add(5*time.Minute)) {
			return nil, invalidEnrollment("capacity metric %q observed_at is invalid", observation.Metric)
		}
		observation.ObservedAt = observation.ObservedAt.UTC()

		canonicalTarget, err := validateObservationTarget(observation, nodeID, hardware, storage, network)
		if err != nil {
			return nil, err
		}
		observation.TargetID = canonicalTarget
		key := string(observation.Metric) + "\x00" + strings.ToLower(observation.TargetID) + "\x00" + observation.ObservedAt.Format(time.RFC3339Nano)
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidEnrollment("duplicate capacity observation for metric %q and target %q", observation.Metric, observation.TargetID)
		}
		seen[key] = struct{}{}
		result = append(result, observation)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Metric != result[j].Metric {
			return result[i].Metric < result[j].Metric
		}
		if strings.ToLower(result[i].TargetID) != strings.ToLower(result[j].TargetID) {
			return strings.ToLower(result[i].TargetID) < strings.ToLower(result[j].TargetID)
		}
		return result[i].ObservedAt.Before(result[j].ObservedAt)
	})
	return result, nil
}

func validateObservationTarget(
	observation CapacityObservation,
	nodeID string,
	hardware HardwareInventory,
	storage map[string]StorageDevice,
	network map[string]NetworkInterface,
) (string, error) {
	switch observation.Metric {
	case MetricCPUUtilization, MetricMemoryUsed:
		if !strings.EqualFold(observation.TargetID, nodeID) {
			return "", invalidEnrollment("capacity metric %q must target node_id", observation.Metric)
		}
		if observation.Metric == MetricMemoryUsed && observation.Value > float64(hardware.MemoryBytes) {
			return "", invalidEnrollment("memory usage observation exceeds installed memory")
		}
		return nodeID, nil
	case MetricStorageUsed, MetricStorageIOPS, MetricStorageLatency, MetricStorageQueueDepth:
		device, exists := storage[strings.ToLower(observation.TargetID)]
		if !exists {
			return "", invalidEnrollment("capacity metric %q targets unknown storage %q", observation.Metric, observation.TargetID)
		}
		if observation.Metric == MetricStorageUsed && observation.Value > float64(device.CapacityBytes) {
			return "", invalidEnrollment("storage usage observation exceeds device %q capacity", device.ID)
		}
		return device.ID, nil
	case MetricNetworkThroughput, MetricNetworkLatency, MetricNetworkPacketLoss:
		networkInterface, exists := network[strings.ToLower(observation.TargetID)]
		if !exists {
			return "", invalidEnrollment("capacity metric %q targets unknown interface %q", observation.Metric, observation.TargetID)
		}
		if observation.Metric == MetricNetworkThroughput && networkInterface.LinkSpeedMbps > 0 && observation.Value > float64(networkInterface.LinkSpeedMbps)*1_000_000 {
			return "", invalidEnrollment("network throughput exceeds interface %q link speed", networkInterface.ID)
		}
		return networkInterface.ID, nil
	case MetricDatabaseLatency, MetricDatabaseTransactions, MetricServiceQueueDepth:
		if err := validateIdentifier("capacity_observations.target_id", observation.TargetID, 128); err != nil {
			return "", err
		}
		return observation.TargetID, nil
	default:
		return "", invalidEnrollment("unsupported capacity metric %q", observation.Metric)
	}
}

// EvaluateEnrollmentPreconditions validates and normalizes its input before
// evaluating it. Invalid input therefore always fails closed. Freshness and
// certificate validity are evaluated at collected_at, not at wall-clock time.
// The function never mutates enrollment or network state.
func EvaluateEnrollmentPreconditions(request EnrollmentRequest) EnrollmentPreconditions {
	normalized, err := NormalizeEnrollment(request)
	if err != nil {
		return EnrollmentPreconditions{
			Ready: false,
			Checks: []EnrollmentPrecondition{
				{Code: "contract.valid", Satisfied: false, Message: "enrollment contract is invalid"},
				{Code: "effects.network-mutation.disabled", Satisfied: true},
			},
		}
	}
	return evaluateNormalizedEnrollmentPreconditions(normalized)
}

func evaluateNormalizedEnrollmentPreconditions(request EnrollmentRequest) EnrollmentPreconditions {
	if request.ContractVersion != EnrollmentContractV2 {
		return EnrollmentPreconditions{
			Ready: false,
			Checks: []EnrollmentPrecondition{
				{Code: "contract.v2", Satisfied: false, Message: "v2 contract is required for admission"},
				{Code: "effects.network-mutation.disabled", Satisfied: true},
			},
		}
	}

	collectedAt := time.Time{}
	if request.CollectedAt != nil {
		collectedAt = *request.CollectedAt
	}
	certificateValid := request.Identity != nil && !collectedAt.IsZero() &&
		!collectedAt.Before(request.Identity.Certificate.NotBefore) && collectedAt.Before(request.Identity.Certificate.NotAfter)
	managementPath := false
	for _, networkInterface := range request.NetworkInterfaces {
		if managementPathZone(networkInterface.Zone) && networkInterface.OperationalState == LinkUp && len(networkInterface.Addresses) > 0 {
			managementPath = true
			break
		}
	}

	requiredMetrics := map[CapacityMetric]bool{
		MetricCPUUtilization:    false,
		MetricMemoryUsed:        false,
		MetricStorageUsed:       false,
		MetricNetworkThroughput: false,
	}
	if hasRole(request.Roles, corecontracts.RoleDataNode) {
		requiredMetrics[MetricDatabaseLatency] = false
	}
	allFresh := len(request.CapacityObservations) > 0
	allMeasured := len(request.CapacityObservations) > 0
	for _, observation := range request.CapacityObservations {
		if _, required := requiredMetrics[observation.Metric]; required && observation.Evidence == EvidenceMeasured {
			requiredMetrics[observation.Metric] = true
		}
		if collectedAt.IsZero() || observation.ObservedAt.After(collectedAt) || collectedAt.Sub(observation.ObservedAt) > capacityFreshnessWindow {
			allFresh = false
		}
		if observation.Evidence != EvidenceMeasured {
			allMeasured = false
		}
	}
	coverage := true
	for _, present := range requiredMetrics {
		coverage = coverage && present
	}
	inventoryCapability := false
	for _, capability := range request.Capabilities {
		if capability == "inventory" {
			inventoryCapability = true
			break
		}
	}
	agentRole := hasRole(request.Roles, corecontracts.RoleAgent)

	checks := []EnrollmentPrecondition{
		{Code: "contract.v2", Satisfied: true},
		{Code: "role.agent", Satisfied: agentRole, Message: failedMessage(agentRole, "agent role is required")},
		{Code: "capability.inventory", Satisfied: inventoryCapability, Message: failedMessage(inventoryCapability, "inventory capability is required")},
		{Code: "identity.certificate.window-valid-at-collection", Satisfied: certificateValid, Message: failedMessage(certificateValid, "node certificate validity window does not include collected_at")},
		{Code: "network.management-path.observed", Satisfied: managementPath, Message: failedMessage(managementPath, "an up management, LAN, or trusted interface with an address is required")},
		{Code: "capacity.required-metrics.measured", Satisfied: coverage, Message: failedMessage(coverage, "required measured capacity metrics are missing")},
		{Code: "capacity.observations.fresh", Satisfied: allFresh, Message: failedMessage(allFresh, "capacity observations must be no more than 15 minutes old")},
		{Code: "capacity.observations.measured", Satisfied: allMeasured, Message: failedMessage(allMeasured, "enrollment readiness requires measured capacity evidence")},
		{Code: "effects.network-mutation.disabled", Satisfied: true},
	}
	ready := true
	for _, check := range checks {
		ready = ready && check.Satisfied
	}
	return EnrollmentPreconditions{Ready: ready, Checks: checks}
}

func hasRole(roles []corecontracts.NodeRole, role corecontracts.NodeRole) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func managementPathZone(zone NetworkZone) bool {
	return zone == ZoneManagement || zone == ZoneLAN || zone == ZoneTrusted
}

func failedMessage(satisfied bool, message string) string {
	if satisfied {
		return ""
	}
	return message
}
