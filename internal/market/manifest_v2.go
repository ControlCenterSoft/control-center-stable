package market

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	ManifestSchemaV1 = "market.manifest/v1"
	ManifestSchemaV2 = "market.manifest/v2"
)

type ManifestStatus string

const (
	ManifestStatusTransitional    ManifestStatus = "TRANSITIONAL"
	ManifestStatusProductionReady ManifestStatus = "PRODUCTION_READY"
)

type WorkloadType string

const (
	WorkloadUnspecified WorkloadType = "UNSPECIFIED"
	WorkloadStateless   WorkloadType = "STATELESS"
	WorkloadStateful    WorkloadType = "STATEFUL"
	WorkloadClustered   WorkloadType = "CLUSTERED"
	WorkloadSingleton   WorkloadType = "SINGLETON"
)

type LifecycleOperationV2 string

const (
	LifecycleV2Install        LifecycleOperationV2 = "install"
	LifecycleV2Configure      LifecycleOperationV2 = "configure"
	LifecycleV2Health         LifecycleOperationV2 = "health"
	LifecycleV2Capacity       LifecycleOperationV2 = "capacity"
	LifecycleV2ScalePlacement LifecycleOperationV2 = "scale_placement"
	LifecycleV2Update         LifecycleOperationV2 = "update"
	LifecycleV2Migrate        LifecycleOperationV2 = "migrate"
	LifecycleV2Drain          LifecycleOperationV2 = "drain"
	LifecycleV2Backup         LifecycleOperationV2 = "backup"
	LifecycleV2Restore        LifecycleOperationV2 = "restore"
	LifecycleV2Failover       LifecycleOperationV2 = "failover"
	LifecycleV2Remove         LifecycleOperationV2 = "remove"
)

type LifecycleSpec struct {
	Operations []LifecycleOperationV2 `json:"operations"`
}

type CapacityStatus string

const (
	CapacityUnverified CapacityStatus = "UNVERIFIED"
	CapacityEstimated  CapacityStatus = "ESTIMATED"
	CapacityCertified  CapacityStatus = "CERTIFIED"
)

type CapacityConfidence string

const (
	CapacityConfidenceUnknown CapacityConfidence = "UNKNOWN"
	CapacityConfidenceLow     CapacityConfidence = "LOW"
	CapacityConfidenceMedium  CapacityConfidence = "MEDIUM"
	CapacityConfidenceHigh    CapacityConfidence = "HIGH"
)

type CapacityEvidenceKind string

const (
	CapacityEvidenceBenchmark CapacityEvidenceKind = "BENCHMARK"
	CapacityEvidenceTelemetry CapacityEvidenceKind = "TELEMETRY"
	CapacityEvidenceLoadTest  CapacityEvidenceKind = "LOAD_TEST"
)

// ResourceEnvelope is the minimum reserved capacity for one module instance.
// Values are deliberately integer based so manifests cannot contain NaN or
// implementation-dependent floating point claims.
type ResourceEnvelope struct {
	CPUMillicores       uint64 `json:"cpu_millicores"`
	MemoryMiB           uint64 `json:"memory_mib"`
	StorageMiB          uint64 `json:"storage_mib"`
	StorageIOPS         uint64 `json:"storage_iops"`
	NetworkMbps         uint64 `json:"network_mbps"`
	DatabaseConnections uint64 `json:"database_connections"`
	ServiceQueueDepth   uint64 `json:"service_queue_depth"`
}

type CapacityEvidence struct {
	ID        string               `json:"id"`
	Kind      CapacityEvidenceKind `json:"kind"`
	Reference string               `json:"reference"`
	Digest    string               `json:"digest"`
}

// CapacityProfile does not treat an undocumented maximum as evidence. An
// UNVERIFIED profile must carry no capacity claims. ESTIMATED and CERTIFIED
// profiles require reproducible evidence and an identified benchmark scenario.
type CapacityProfile struct {
	Status            CapacityStatus     `json:"status"`
	WorkloadUnit      string             `json:"workload_unit"`
	Minimum           ResourceEnvelope   `json:"minimum"`
	SafeUnits         uint64             `json:"safe_units"`
	TechnicalLimit    uint64             `json:"technical_limit"`
	Confidence        CapacityConfidence `json:"confidence"`
	BenchmarkScenario string             `json:"benchmark_scenario"`
	Evidence          []CapacityEvidence `json:"evidence"`
}

type RecoveryCapability string

const (
	RecoveryUndeclared    RecoveryCapability = "UNDECLARED"
	RecoveryNotApplicable RecoveryCapability = "NOT_APPLICABLE"
	RecoverySupported     RecoveryCapability = "SUPPORTED"
)

type RecoverySpec struct {
	Adapter         string             `json:"adapter,omitempty"`
	Backup          RecoveryCapability `json:"backup"`
	Restore         RecoveryCapability `json:"restore"`
	Failover        RecoveryCapability `json:"failover"`
	FencingRequired bool               `json:"fencing_required"`
	RPOSeconds      *uint64            `json:"rpo_seconds,omitempty"`
	RTOSeconds      *uint64            `json:"rto_seconds,omitempty"`
}

type NetworkEnforcement string

const NetworkPolicyControlled NetworkEnforcement = "POLICY_CONTROLLED"

type NetworkProtocol string

const (
	NetworkTCP NetworkProtocol = "TCP"
	NetworkUDP NetworkProtocol = "UDP"
)

type NetworkDirection string

const (
	NetworkIngress NetworkDirection = "INGRESS"
	NetworkEgress  NetworkDirection = "EGRESS"
)

type NetworkExposure string

const (
	NetworkInternal NetworkExposure = "INTERNAL"
	NetworkExternal NetworkExposure = "EXTERNAL"
)

type PortRange struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

// NetworkRequirement is declarative. It is never an instruction to modify a
// firewall, routing, NAT, or port-forwarding configuration.
type NetworkRequirement struct {
	Name      string           `json:"name"`
	Protocol  NetworkProtocol  `json:"protocol"`
	Direction NetworkDirection `json:"direction"`
	Exposure  NetworkExposure  `json:"exposure"`
	Zones     []string         `json:"zones"`
	Ports     []PortRange      `json:"ports"`
}

type NetworkSpec struct {
	Enforcement  NetworkEnforcement   `json:"enforcement"`
	Requirements []NetworkRequirement `json:"requirements"`
}

type DependencyKind string

const (
	DependencyRequired DependencyKind = "REQUIRED"
	DependencyOptional DependencyKind = "OPTIONAL"
)

type ModuleRequirement struct {
	ID                string         `json:"id"`
	VersionConstraint string         `json:"version_constraint"`
	Kind              DependencyKind `json:"kind"`
}

type StorageClass string

const (
	StorageFilesystem StorageClass = "FILESYSTEM"
	StorageBlock      StorageClass = "BLOCK"
	StorageObject     StorageClass = "OBJECT"
	StorageDatabase   StorageClass = "DATABASE"
)

type StorageAccessMode string

const (
	StorageReadWriteOnce StorageAccessMode = "READ_WRITE_ONCE"
	StorageReadWriteMany StorageAccessMode = "READ_WRITE_MANY"
)

type StorageRequirement struct {
	Name       string            `json:"name"`
	Class      StorageClass      `json:"class"`
	AccessMode StorageAccessMode `json:"access_mode"`
	MinimumMiB uint64            `json:"minimum_mib"`
	Persistent bool              `json:"persistent"`
}

type SecretScope string

const (
	SecretModule   SecretScope = "MODULE"
	SecretInstance SecretScope = "INSTANCE"
)

type SecretRequirement struct {
	Name    string      `json:"name"`
	Purpose string      `json:"purpose"`
	Scope   SecretScope `json:"scope"`
}

type DependencyMetadata struct {
	Modules      []ModuleRequirement  `json:"modules"`
	Capabilities []string             `json:"capabilities"`
	Storage      []StorageRequirement `json:"storage"`
	Secrets      []SecretRequirement  `json:"secrets"`
}

type CompatibilitySpec struct {
	Platforms            []string `json:"platforms"`
	Architectures        []string `json:"architectures"`
	MinControllerVersion string   `json:"min_controller_version,omitempty"`
	MaxControllerVersion string   `json:"max_controller_version,omitempty"`
}

type ActivationMode string

const ActivationExplicit ActivationMode = "EXPLICIT"

type NetworkChangePolicy string

const NetworkApprovalRequired NetworkChangePolicy = "POLICY_APPROVAL_REQUIRED"

// ActivationPolicy makes installation and network authorization separate,
// explicit operator decisions. A manifest cannot opt into implicit activation.
type ActivationPolicy struct {
	Mode           ActivationMode      `json:"mode"`
	NetworkChanges NetworkChangePolicy `json:"network_changes"`
}

type MigrationStrategy string

const MigrationPreserveDeclarations MigrationStrategy = "PRESERVE_DECLARATIONS"

type ManifestMigration struct {
	SourceSchemaVersion string            `json:"source_schema_version"`
	Strategy            MigrationStrategy `json:"strategy"`
	Warnings            []string          `json:"warnings"`
}

type ManifestV2 struct {
	SchemaVersion string             `json:"schema_version"`
	ID            string             `json:"id"`
	Version       string             `json:"version"`
	Status        ManifestStatus     `json:"status"`
	Capabilities  []string           `json:"capabilities"`
	Providers     []string           `json:"providers"`
	WorkloadType  WorkloadType       `json:"workload_type"`
	Lifecycle     LifecycleSpec      `json:"lifecycle"`
	Capacity      CapacityProfile    `json:"capacity"`
	Recovery      RecoverySpec       `json:"recovery"`
	Network       NetworkSpec        `json:"network"`
	Dependencies  DependencyMetadata `json:"dependencies"`
	Compatibility CompatibilitySpec  `json:"compatibility"`
	Activation    ActivationPolicy   `json:"activation"`
	Migration     *ManifestMigration `json:"migration,omitempty"`
}

type ManifestValidationError struct {
	Field   string
	Message string
}

func (e *ManifestValidationError) Error() string {
	return fmt.Sprintf("invalid market manifest v2 %s: %s", e.Field, e.Message)
}

var (
	manifestIdentifierPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$`)
	manifestZonePattern       = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,31}$`)
	manifestDigestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	versionComparatorPattern  = regexp.MustCompile(`^(?:=|>=|<=|>|<|\^|~)?[0-9]+\.[0-9]+\.[0-9]+$`)
	versionComparatorParts    = regexp.MustCompile(`^(=|>=|<=|>|<|\^|~)?([0-9]+\.[0-9]+\.[0-9]+)$`)
)

func validationError(field, message string) error {
	return &ManifestValidationError{Field: field, Message: message}
}

// IsValidManifestID reports whether id is a canonical Market manifest ID.
func IsValidManifestID(id string) bool {
	return manifestIdentifierPattern.MatchString(id)
}

func ValidateManifestV2(manifest ManifestV2) error {
	if manifest.SchemaVersion != ManifestSchemaV2 {
		return validationError("schema_version", "must be "+ManifestSchemaV2)
	}
	if !manifestIdentifierPattern.MatchString(manifest.ID) {
		return validationError("id", "must be a lowercase manifest identifier")
	}
	if !manifestVersionPattern.MatchString(manifest.Version) {
		return validationError("version", "must be a semantic version")
	}
	if manifest.Status != ManifestStatusTransitional && manifest.Status != ManifestStatusProductionReady {
		return validationError("status", "unsupported status")
	}
	if err := validateIdentifierSet("capabilities", manifest.Capabilities, true); err != nil {
		return err
	}
	if err := validateIdentifierSet("providers", manifest.Providers, false); err != nil {
		return err
	}
	if !validWorkloadType(manifest.WorkloadType) {
		return validationError("workload_type", "unsupported workload type")
	}
	if manifest.Status == ManifestStatusProductionReady && manifest.WorkloadType == WorkloadUnspecified {
		return validationError("workload_type", "production-ready manifest must declare a workload type")
	}
	if err := validateLifecycle(manifest); err != nil {
		return err
	}
	if err := validateCapacity(manifest); err != nil {
		return err
	}
	if err := validateRecovery(manifest); err != nil {
		return err
	}
	if err := validateNetwork(manifest.Network); err != nil {
		return err
	}
	if err := validateDependencies(manifest.ID, manifest.Dependencies); err != nil {
		return err
	}
	if err := validateCompatibility(manifest); err != nil {
		return err
	}
	if manifest.Activation.Mode != ActivationExplicit {
		return validationError("activation.mode", "only explicit activation is permitted")
	}
	if manifest.Activation.NetworkChanges != NetworkApprovalRequired {
		return validationError("activation.network_changes", "network changes require policy approval")
	}
	if err := validateMigration(manifest.Migration); err != nil {
		return err
	}
	return nil
}

func DecodeManifestV2(raw []byte) (ManifestV2, error) {
	if err := rejectDuplicateJSONFields(raw); err != nil {
		return ManifestV2{}, fmt.Errorf("decode market manifest v2: %w", err)
	}
	var manifest ManifestV2
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return ManifestV2{}, fmt.Errorf("decode market manifest v2: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return ManifestV2{}, fmt.Errorf("decode market manifest v2: multiple JSON values are not allowed")
		}
		return ManifestV2{}, fmt.Errorf("decode market manifest v2: trailing data: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return ManifestV2{}, fmt.Errorf("decode market manifest v2: top-level JSON value must be an object")
	}
	for _, field := range []string{
		"schema_version", "id", "version", "status", "capabilities", "providers", "workload_type",
		"lifecycle", "capacity", "recovery", "network", "dependencies", "compatibility", "activation",
	} {
		if _, exists := fields[field]; !exists {
			return ManifestV2{}, validationError(field, "field is required")
		}
	}
	if err := ValidateManifestV2(manifest); err != nil {
		return ManifestV2{}, err
	}
	return manifest, nil
}

func rejectDuplicateJSONFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSONValue(decoder, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate field %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("invalid object closing token at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := walkJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid array closing token at %s", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}

func validateIdentifierSet(field string, values []string, required bool) error {
	if required && len(values) == 0 {
		return validationError(field, "must not be empty")
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !manifestIdentifierPattern.MatchString(value) {
			return validationError(fmt.Sprintf("%s[%d]", field, index), "must be a lowercase identifier")
		}
		if _, exists := seen[value]; exists {
			return validationError(field, "contains duplicate "+value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validWorkloadType(value WorkloadType) bool {
	switch value {
	case WorkloadUnspecified, WorkloadStateless, WorkloadStateful, WorkloadClustered, WorkloadSingleton:
		return true
	default:
		return false
	}
}

func validateLifecycle(manifest ManifestV2) error {
	if len(manifest.Lifecycle.Operations) == 0 {
		return validationError("lifecycle.operations", "must not be empty")
	}
	seen := make(map[LifecycleOperationV2]struct{}, len(manifest.Lifecycle.Operations))
	for index, operation := range manifest.Lifecycle.Operations {
		if !validLifecycleOperation(operation) {
			return validationError(fmt.Sprintf("lifecycle.operations[%d]", index), "unsupported operation")
		}
		if _, exists := seen[operation]; exists {
			return validationError("lifecycle.operations", "contains duplicate "+string(operation))
		}
		seen[operation] = struct{}{}
	}
	if manifest.Status != ManifestStatusProductionReady {
		return nil
	}
	required := []LifecycleOperationV2{
		LifecycleV2Install, LifecycleV2Configure, LifecycleV2Health, LifecycleV2Capacity,
		LifecycleV2Update, LifecycleV2Backup, LifecycleV2Restore, LifecycleV2Remove,
	}
	if manifest.WorkloadType == WorkloadStateful || manifest.WorkloadType == WorkloadClustered {
		required = append(required, LifecycleV2Migrate, LifecycleV2Drain)
	}
	if manifest.WorkloadType == WorkloadClustered {
		required = append(required, LifecycleV2Failover)
	}
	for _, operation := range required {
		if _, exists := seen[operation]; !exists {
			return validationError("lifecycle.operations", "production-ready manifest is missing "+string(operation))
		}
	}
	return nil
}

func validLifecycleOperation(operation LifecycleOperationV2) bool {
	switch operation {
	case LifecycleV2Install, LifecycleV2Configure, LifecycleV2Health, LifecycleV2Capacity,
		LifecycleV2ScalePlacement, LifecycleV2Update, LifecycleV2Migrate, LifecycleV2Drain,
		LifecycleV2Backup, LifecycleV2Restore, LifecycleV2Failover, LifecycleV2Remove:
		return true
	default:
		return false
	}
}

func validateCapacity(manifest ManifestV2) error {
	profile := manifest.Capacity
	if !manifestIdentifierPattern.MatchString(profile.WorkloadUnit) {
		return validationError("capacity.workload_unit", "must be a lowercase identifier")
	}
	switch profile.Status {
	case CapacityUnverified:
		if profile.SafeUnits != 0 || profile.TechnicalLimit != 0 || profile.Confidence != CapacityConfidenceUnknown || profile.BenchmarkScenario != "" || len(profile.Evidence) != 0 {
			return validationError("capacity", "unverified profile must not contain capacity claims or evidence")
		}
	case CapacityEstimated, CapacityCertified:
		if profile.SafeUnits == 0 || profile.TechnicalLimit == 0 || profile.SafeUnits > profile.TechnicalLimit {
			return validationError("capacity", "safe units and technical limit must be positive and ordered")
		}
		if profile.Confidence != CapacityConfidenceLow && profile.Confidence != CapacityConfidenceMedium && profile.Confidence != CapacityConfidenceHigh {
			return validationError("capacity.confidence", "verified claim requires LOW, MEDIUM, or HIGH confidence")
		}
		if !manifestIdentifierPattern.MatchString(profile.BenchmarkScenario) {
			return validationError("capacity.benchmark_scenario", "verified claim requires a benchmark identifier")
		}
		if len(profile.Evidence) == 0 {
			return validationError("capacity.evidence", "verified claim requires evidence")
		}
		if profile.Minimum == (ResourceEnvelope{}) {
			return validationError("capacity.minimum", "verified claim requires a non-zero resource envelope")
		}
	default:
		return validationError("capacity.status", "unsupported capacity status")
	}
	seenEvidence := make(map[string]struct{}, len(profile.Evidence))
	for index, evidence := range profile.Evidence {
		prefix := fmt.Sprintf("capacity.evidence[%d]", index)
		if !manifestIdentifierPattern.MatchString(evidence.ID) {
			return validationError(prefix+".id", "must be a lowercase identifier")
		}
		if _, exists := seenEvidence[evidence.ID]; exists {
			return validationError("capacity.evidence", "contains duplicate "+evidence.ID)
		}
		seenEvidence[evidence.ID] = struct{}{}
		if evidence.Kind != CapacityEvidenceBenchmark && evidence.Kind != CapacityEvidenceTelemetry && evidence.Kind != CapacityEvidenceLoadTest {
			return validationError(prefix+".kind", "unsupported evidence kind")
		}
		if strings.TrimSpace(evidence.Reference) == "" || len(evidence.Reference) > 512 {
			return validationError(prefix+".reference", "must contain a bounded reference")
		}
		if !manifestDigestPattern.MatchString(evidence.Digest) {
			return validationError(prefix+".digest", "must be a sha256 digest")
		}
	}
	if manifest.Status == ManifestStatusProductionReady && profile.Status == CapacityUnverified {
		return validationError("capacity.status", "production-ready manifest requires an evidenced capacity profile")
	}
	return nil
}

func validateRecovery(manifest ManifestV2) error {
	recovery := manifest.Recovery
	for _, item := range []struct {
		field      string
		capability RecoveryCapability
	}{
		{field: "recovery.backup", capability: recovery.Backup},
		{field: "recovery.restore", capability: recovery.Restore},
		{field: "recovery.failover", capability: recovery.Failover},
	} {
		if item.capability != RecoveryUndeclared && item.capability != RecoveryNotApplicable && item.capability != RecoverySupported {
			return validationError(item.field, "unsupported recovery capability")
		}
	}
	if recovery.Adapter != "" && !manifestIdentifierPattern.MatchString(recovery.Adapter) {
		return validationError("recovery.adapter", "must be a lowercase identifier")
	}
	operations := lifecycleOperationSet(manifest.Lifecycle.Operations)
	if recovery.Backup == RecoverySupported && !operations[LifecycleV2Backup] {
		return validationError("recovery.backup", "SUPPORTED requires the backup lifecycle operation")
	}
	if recovery.Restore == RecoverySupported && !operations[LifecycleV2Restore] {
		return validationError("recovery.restore", "SUPPORTED requires the restore lifecycle operation")
	}
	if recovery.Failover == RecoverySupported && !operations[LifecycleV2Failover] {
		return validationError("recovery.failover", "SUPPORTED requires the failover lifecycle operation")
	}
	if operations[LifecycleV2Backup] && recovery.Backup != RecoverySupported {
		return validationError("recovery.backup", "backup lifecycle operation requires SUPPORTED recovery metadata")
	}
	if operations[LifecycleV2Restore] && recovery.Restore != RecoverySupported {
		return validationError("recovery.restore", "restore lifecycle operation requires SUPPORTED recovery metadata")
	}
	if operations[LifecycleV2Failover] && recovery.Failover != RecoverySupported {
		return validationError("recovery.failover", "failover lifecycle operation requires SUPPORTED recovery metadata")
	}
	stateful := manifest.WorkloadType == WorkloadStateful || manifest.WorkloadType == WorkloadClustered
	if recovery.RPOSeconds != nil && *recovery.RPOSeconds == 0 {
		return validationError("recovery.rpo_seconds", "must be positive")
	}
	if recovery.RTOSeconds != nil && *recovery.RTOSeconds == 0 {
		return validationError("recovery.rto_seconds", "must be positive")
	}
	if (recovery.RPOSeconds != nil || recovery.RTOSeconds != nil) && recovery.Backup != RecoverySupported && recovery.Restore != RecoverySupported {
		return validationError("recovery", "RPO or RTO requires supported backup or restore")
	}
	if stateful && (recovery.Backup == RecoverySupported || recovery.Restore == RecoverySupported || recovery.Failover == RecoverySupported) && recovery.Adapter == "" {
		return validationError("recovery.adapter", "stateful recovery requires an adapter")
	}
	if stateful && manifest.Status == ManifestStatusProductionReady {
		if recovery.RPOSeconds == nil || recovery.RTOSeconds == nil || *recovery.RPOSeconds == 0 || *recovery.RTOSeconds == 0 {
			return validationError("recovery", "production-ready stateful workload requires positive RPO and RTO")
		}
	}
	if recovery.Failover == RecoverySupported && stateful && !recovery.FencingRequired {
		return validationError("recovery.fencing_required", "stateful failover requires fencing")
	}
	if recovery.FencingRequired && recovery.Failover != RecoverySupported {
		return validationError("recovery.fencing_required", "fencing may only be required for supported failover")
	}
	return nil
}

func lifecycleOperationSet(operations []LifecycleOperationV2) map[LifecycleOperationV2]bool {
	result := make(map[LifecycleOperationV2]bool, len(operations))
	for _, operation := range operations {
		result[operation] = true
	}
	return result
}

func validateNetwork(network NetworkSpec) error {
	if network.Enforcement != NetworkPolicyControlled {
		return validationError("network.enforcement", "requirements must remain policy controlled")
	}
	seen := make(map[string]struct{}, len(network.Requirements))
	for index, requirement := range network.Requirements {
		prefix := fmt.Sprintf("network.requirements[%d]", index)
		if !manifestIdentifierPattern.MatchString(requirement.Name) {
			return validationError(prefix+".name", "must be a lowercase identifier")
		}
		if _, exists := seen[requirement.Name]; exists {
			return validationError("network.requirements", "contains duplicate "+requirement.Name)
		}
		seen[requirement.Name] = struct{}{}
		if requirement.Protocol != NetworkTCP && requirement.Protocol != NetworkUDP {
			return validationError(prefix+".protocol", "unsupported protocol")
		}
		if requirement.Direction != NetworkIngress && requirement.Direction != NetworkEgress {
			return validationError(prefix+".direction", "unsupported direction")
		}
		if requirement.Exposure != NetworkInternal && requirement.Exposure != NetworkExternal {
			return validationError(prefix+".exposure", "unsupported exposure")
		}
		if len(requirement.Zones) == 0 {
			return validationError(prefix+".zones", "must not be empty")
		}
		zones := make(map[string]struct{}, len(requirement.Zones))
		for zoneIndex, zone := range requirement.Zones {
			if !manifestZonePattern.MatchString(zone) {
				return validationError(fmt.Sprintf("%s.zones[%d]", prefix, zoneIndex), "must be an uppercase network zone")
			}
			if _, exists := zones[zone]; exists {
				return validationError(prefix+".zones", "contains duplicate "+zone)
			}
			zones[zone] = struct{}{}
		}
		if len(requirement.Ports) == 0 {
			return validationError(prefix+".ports", "must not be empty")
		}
		ports := append([]PortRange(nil), requirement.Ports...)
		sort.Slice(ports, func(i, j int) bool {
			if ports[i].From == ports[j].From {
				return ports[i].To < ports[j].To
			}
			return ports[i].From < ports[j].From
		})
		for portIndex, port := range ports {
			if port.From == 0 || port.To == 0 || port.From > port.To {
				return validationError(prefix+".ports", "contains an invalid port range")
			}
			if portIndex > 0 && ports[portIndex-1].To >= port.From {
				return validationError(prefix+".ports", "contains overlapping port ranges")
			}
		}
	}
	return nil
}

func validateDependencies(manifestID string, dependencies DependencyMetadata) error {
	seenModules := make(map[string]struct{}, len(dependencies.Modules))
	for index, dependency := range dependencies.Modules {
		prefix := fmt.Sprintf("dependencies.modules[%d]", index)
		if !manifestIdentifierPattern.MatchString(dependency.ID) {
			return validationError(prefix+".id", "must be a lowercase identifier")
		}
		if dependency.ID == manifestID {
			return validationError(prefix+".id", "manifest cannot depend on itself")
		}
		if _, exists := seenModules[dependency.ID]; exists {
			return validationError("dependencies.modules", "contains duplicate "+dependency.ID)
		}
		seenModules[dependency.ID] = struct{}{}
		if !validVersionConstraint(dependency.VersionConstraint) {
			return validationError(prefix+".version_constraint", "must contain semantic-version comparators")
		}
		if dependency.Kind != DependencyRequired && dependency.Kind != DependencyOptional {
			return validationError(prefix+".kind", "unsupported dependency kind")
		}
	}
	if err := validateIdentifierSet("dependencies.capabilities", dependencies.Capabilities, false); err != nil {
		return err
	}
	seenStorage := make(map[string]struct{}, len(dependencies.Storage))
	for index, storage := range dependencies.Storage {
		prefix := fmt.Sprintf("dependencies.storage[%d]", index)
		if !manifestIdentifierPattern.MatchString(storage.Name) {
			return validationError(prefix+".name", "must be a lowercase identifier")
		}
		if _, exists := seenStorage[storage.Name]; exists {
			return validationError("dependencies.storage", "contains duplicate "+storage.Name)
		}
		seenStorage[storage.Name] = struct{}{}
		if storage.Class != StorageFilesystem && storage.Class != StorageBlock && storage.Class != StorageObject && storage.Class != StorageDatabase {
			return validationError(prefix+".class", "unsupported storage class")
		}
		if storage.AccessMode != StorageReadWriteOnce && storage.AccessMode != StorageReadWriteMany {
			return validationError(prefix+".access_mode", "unsupported storage access mode")
		}
		if storage.MinimumMiB == 0 {
			return validationError(prefix+".minimum_mib", "must be positive")
		}
	}
	seenSecrets := make(map[string]struct{}, len(dependencies.Secrets))
	for index, secret := range dependencies.Secrets {
		prefix := fmt.Sprintf("dependencies.secrets[%d]", index)
		if !manifestIdentifierPattern.MatchString(secret.Name) {
			return validationError(prefix+".name", "must be a lowercase identifier")
		}
		if _, exists := seenSecrets[secret.Name]; exists {
			return validationError("dependencies.secrets", "contains duplicate "+secret.Name)
		}
		seenSecrets[secret.Name] = struct{}{}
		if strings.TrimSpace(secret.Purpose) == "" || len(secret.Purpose) > 256 {
			return validationError(prefix+".purpose", "must contain a bounded purpose")
		}
		if secret.Scope != SecretModule && secret.Scope != SecretInstance {
			return validationError(prefix+".scope", "unsupported secret scope")
		}
	}
	return nil
}

func validVersionConstraint(constraint string) bool {
	parts := strings.Fields(constraint)
	if len(parts) == 0 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !versionComparatorPattern.MatchString(part) {
			return false
		}
	}
	if len(parts) == 1 {
		return true
	}
	type bound struct {
		op      string
		version string
	}
	var lower, upper *bound
	for _, part := range parts {
		matches := versionComparatorParts.FindStringSubmatch(part)
		if len(matches) != 3 {
			return false
		}
		candidate := &bound{op: matches[1], version: matches[2]}
		switch candidate.op {
		case ">", ">=":
			if lower != nil {
				return false
			}
			lower = candidate
		case "<", "<=":
			if upper != nil {
				return false
			}
			upper = candidate
		default:
			return false
		}
	}
	if lower == nil || upper == nil {
		return false
	}
	comparison := compareSemanticVersions(lower.version, upper.version)
	return comparison < 0 || comparison == 0 && lower.op == ">=" && upper.op == "<="
}

func validateCompatibility(manifest ManifestV2) error {
	compatibility := manifest.Compatibility
	if len(compatibility.Platforms) == 0 {
		return validationError("compatibility.platforms", "must not be empty")
	}
	seenPlatforms := map[string]struct{}{}
	for index, platform := range compatibility.Platforms {
		if platform != "linux" && platform != "windows" {
			return validationError(fmt.Sprintf("compatibility.platforms[%d]", index), "unsupported platform")
		}
		if _, exists := seenPlatforms[platform]; exists {
			return validationError("compatibility.platforms", "contains duplicate "+platform)
		}
		seenPlatforms[platform] = struct{}{}
	}
	seenArchitectures := map[string]struct{}{}
	for index, architecture := range compatibility.Architectures {
		if architecture != "amd64" && architecture != "arm64" {
			return validationError(fmt.Sprintf("compatibility.architectures[%d]", index), "unsupported architecture")
		}
		if _, exists := seenArchitectures[architecture]; exists {
			return validationError("compatibility.architectures", "contains duplicate "+architecture)
		}
		seenArchitectures[architecture] = struct{}{}
	}
	if manifest.Status == ManifestStatusProductionReady && len(compatibility.Architectures) == 0 {
		return validationError("compatibility.architectures", "production-ready manifest must declare an architecture")
	}
	if compatibility.MinControllerVersion != "" && !manifestVersionPattern.MatchString(compatibility.MinControllerVersion) {
		return validationError("compatibility.min_controller_version", "must be a semantic version")
	}
	if compatibility.MaxControllerVersion != "" && !manifestVersionPattern.MatchString(compatibility.MaxControllerVersion) {
		return validationError("compatibility.max_controller_version", "must be a semantic version")
	}
	if compatibility.MaxControllerVersion != "" && compatibility.MinControllerVersion == "" {
		return validationError("compatibility.min_controller_version", "is required when a maximum is declared")
	}
	if compatibility.MinControllerVersion != "" && compatibility.MaxControllerVersion != "" && compareSemanticVersions(compatibility.MinControllerVersion, compatibility.MaxControllerVersion) > 0 {
		return validationError("compatibility", "minimum controller version exceeds maximum")
	}
	if manifest.Status == ManifestStatusProductionReady && compatibility.MinControllerVersion == "" {
		return validationError("compatibility.min_controller_version", "production-ready manifest must declare a minimum controller version")
	}
	return nil
}

func compareSemanticVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := range leftParts {
		if comparison := compareDecimalStrings(leftParts[index], rightParts[index]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func compareDecimalStrings(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return strings.Compare(left, right)
}

func validateMigration(migration *ManifestMigration) error {
	if migration == nil {
		return nil
	}
	if migration.SourceSchemaVersion != ManifestSchemaV1 {
		return validationError("migration.source_schema_version", "unsupported source schema")
	}
	if migration.Strategy != MigrationPreserveDeclarations {
		return validationError("migration.strategy", "unsupported migration strategy")
	}
	if len(migration.Warnings) == 0 {
		return validationError("migration.warnings", "must disclose information missing from v1")
	}
	seen := make(map[string]struct{}, len(migration.Warnings))
	for index, warning := range migration.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 256 {
			return validationError(fmt.Sprintf("migration.warnings[%d]", index), "must be non-empty and bounded")
		}
		if _, exists := seen[warning]; exists {
			return validationError("migration.warnings", "contains a duplicate warning")
		}
		seen[warning] = struct{}{}
	}
	return nil
}

// MigrateBuiltinManifestV1 performs a conservative deterministic migration.
// Missing v1 declarations are represented as unknown/unverified, never inferred.
func MigrateBuiltinManifestV1(source BuiltinManifest) (ManifestV2, error) {
	if err := ValidateBuiltinManifest(source); err != nil {
		return ManifestV2{}, fmt.Errorf("migrate market manifest v1: %w", err)
	}
	lifecycle := make([]LifecycleOperationV2, 0, len(source.Lifecycle))
	for _, operation := range source.Lifecycle {
		switch operation {
		case LifecycleInstall:
			lifecycle = append(lifecycle, LifecycleV2Install)
		case LifecycleUpgrade:
			lifecycle = append(lifecycle, LifecycleV2Update)
		case LifecycleRemove:
			lifecycle = append(lifecycle, LifecycleV2Remove)
		}
	}
	lifecycle = canonicalLifecycleOperations(lifecycle)
	warnings := []string{
		"capacity profile requires evidence before production readiness",
		"dependencies, storage, and secrets were not declared by v1",
		"network requirements were not declared by v1; no policy changes are activated",
		"recovery capabilities and adapter were not declared by v1",
		"workload type and architectures were not declared by v1",
	}
	sort.Strings(warnings)
	manifest := ManifestV2{
		SchemaVersion: ManifestSchemaV2,
		ID:            strings.TrimSpace(source.ID),
		Version:       strings.TrimSpace(source.Version),
		Status:        ManifestStatusTransitional,
		Capabilities:  canonicalIdentifierSet(source.Capabilities),
		Providers:     canonicalIdentifierSet(source.Providers),
		WorkloadType:  WorkloadUnspecified,
		Lifecycle:     LifecycleSpec{Operations: lifecycle},
		Capacity: CapacityProfile{
			Status:       CapacityUnverified,
			WorkloadUnit: "instance",
			Confidence:   CapacityConfidenceUnknown,
			Evidence:     []CapacityEvidence{},
		},
		Recovery: RecoverySpec{
			Backup: RecoveryUndeclared, Restore: RecoveryUndeclared, Failover: RecoveryUndeclared,
		},
		Network: NetworkSpec{Enforcement: NetworkPolicyControlled, Requirements: []NetworkRequirement{}},
		Dependencies: DependencyMetadata{
			Modules: []ModuleRequirement{}, Capabilities: []string{}, Storage: []StorageRequirement{}, Secrets: []SecretRequirement{},
		},
		Compatibility: CompatibilitySpec{
			Platforms: canonicalIdentifierSet(source.Platforms), Architectures: []string{},
		},
		Activation: ActivationPolicy{Mode: ActivationExplicit, NetworkChanges: NetworkApprovalRequired},
		Migration: &ManifestMigration{
			SourceSchemaVersion: ManifestSchemaV1,
			Strategy:            MigrationPreserveDeclarations,
			Warnings:            warnings,
		},
	}
	if err := ValidateManifestV2(manifest); err != nil {
		return ManifestV2{}, fmt.Errorf("migrate market manifest v1: generated invalid v2 manifest: %w", err)
	}
	return manifest, nil
}

func canonicalIdentifierSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func canonicalLifecycleOperations(operations []LifecycleOperationV2) []LifecycleOperationV2 {
	order := []LifecycleOperationV2{
		LifecycleV2Install, LifecycleV2Configure, LifecycleV2Health, LifecycleV2Capacity,
		LifecycleV2ScalePlacement, LifecycleV2Update, LifecycleV2Migrate, LifecycleV2Drain,
		LifecycleV2Backup, LifecycleV2Restore, LifecycleV2Failover, LifecycleV2Remove,
	}
	seen := make(map[LifecycleOperationV2]struct{}, len(operations))
	for _, operation := range operations {
		seen[operation] = struct{}{}
	}
	result := make([]LifecycleOperationV2, 0, len(seen))
	for _, operation := range order {
		if _, exists := seen[operation]; exists {
			result = append(result, operation)
		}
	}
	return result
}

func BuiltinManifestsV2() ([]ManifestV2, error) {
	sources := BuiltinManifests()
	manifests := make([]ManifestV2, 0, len(sources))
	for _, source := range sources {
		manifest, err := MigrateBuiltinManifestV1(source)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func FindBuiltinManifestV2(id string) (ManifestV2, bool, error) {
	source, ok := FindBuiltinManifest(id)
	if !ok {
		return ManifestV2{}, false, nil
	}
	manifest, err := MigrateBuiltinManifestV1(source)
	if err != nil {
		return ManifestV2{}, false, err
	}
	return manifest, true, nil
}
