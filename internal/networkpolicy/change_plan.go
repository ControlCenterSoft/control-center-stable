package networkpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ChangePlanSchemaVersion = "network.change.plan/v1"

const (
	maxInterfacesPerPlan = 64
	maxForwardingIntents = 128
	maxProbesPerPlan     = 128
	maxPhaseTimeout      = 15 * time.Minute
)

var (
	ErrInvalidChangePlan = errors.New("invalid network change plan")
	identifierPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)
)

// ChangeStage is the closed, provider-neutral staged network change sequence.
// The contract describes orchestration only and does not mutate host networking.
type ChangeStage string

const (
	ChangeStageSnapshot    ChangeStage = "snapshot"
	ChangeStagePreflight   ChangeStage = "preflight"
	ChangeStageApplyWindow ChangeStage = "apply_window"
	ChangeStageProbes      ChangeStage = "probes"
	ChangeStageDecision    ChangeStage = "commit_or_rollback"
)

// ChangeAction is a typed adapter capability. It is deliberately not a shell
// command, backend payload, endpoint or credential carrier.
type ChangeAction string

const (
	ActionCaptureRecoverySnapshot ChangeAction = "capture_recovery_snapshot"
	ActionValidateNetworkContract ChangeAction = "validate_network_contract"
	ActionValidateManagementPath  ChangeAction = "validate_management_path"
	ActionAuthorizeForwarding     ChangeAction = "authorize_forwarding"
	ActionOpenTemporaryWindow     ChangeAction = "open_temporary_apply_window"
	ActionRunConnectivityProbes   ChangeAction = "run_connectivity_probes"
	ActionCommitOrRollback        ChangeAction = "commit_or_rollback"
)

type ProbeKind string

const (
	ProbeLinkState        ProbeKind = "link_state"
	ProbeZoneReachability ProbeKind = "zone_reachability"
	ProbeControlPlane     ProbeKind = "control_plane"
)

func (kind ProbeKind) valid() bool {
	switch kind {
	case ProbeLinkState, ProbeZoneReachability, ProbeControlPlane:
		return true
	default:
		return false
	}
}

// InterfaceIntent declares the zone binding visible to safety validation.
// Changed marks interfaces affected by the proposed revision. Including the
// unchanged management path makes multi-NIC safety decisions explicit.
type InterfaceIntent struct {
	InterfaceID string `json:"interface_id"`
	Zone        Zone   `json:"zone"`
	Changed     bool   `json:"changed"`
}

// ConnectivityProbe is a logical probe binding. It has no address or free-form
// command field; concrete adapters resolve the typed target from inventory.
type ConnectivityProbe struct {
	ID          string    `json:"id"`
	Kind        ProbeKind `json:"kind"`
	InterfaceID string    `json:"interface_id"`
	Zone        Zone      `json:"zone"`
}

type TimeoutPolicy struct {
	Snapshot    time.Duration `json:"snapshot"`
	Preflight   time.Duration `json:"preflight"`
	ApplyWindow time.Duration `json:"apply_window"`
	Probe       time.Duration `json:"probe"`
	Rollback    time.Duration `json:"rollback"`
}

type ChangePlanRequest struct {
	NodeID     string              `json:"node_id"`
	RevisionID string              `json:"revision_id"`
	Interfaces []InterfaceIntent   `json:"interfaces"`
	Forwarding []ForwardingIntent  `json:"forwarding"`
	Probes     []ConnectivityProbe `json:"probes"`
	Timeouts   TimeoutPolicy       `json:"timeouts"`
}

type ChangeStep struct {
	Order  int          `json:"order"`
	Stage  ChangeStage  `json:"stage"`
	Action ChangeAction `json:"action"`
}

type ChangePlan struct {
	SchemaVersion         string              `json:"schema_version"`
	PlanID                string              `json:"plan_id"`
	NodeID                string              `json:"node_id"`
	RevisionID            string              `json:"revision_id"`
	Interfaces            []InterfaceIntent   `json:"interfaces"`
	Forwarding            []ForwardingIntent  `json:"forwarding"`
	ForwardingDefaultDeny bool                `json:"forwarding_default_deny"`
	Probes                []ConnectivityProbe `json:"probes"`
	Timeouts              TimeoutPolicy       `json:"timeouts"`
	Steps                 []ChangeStep        `json:"steps"`
}

// DecodeChangePlanRequest performs strict decoding so command, endpoint,
// credential and other unrecognized fields cannot enter the contract.
func DecodeChangePlanRequest(document []byte) (ChangePlanRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var request ChangePlanRequest
	if err := decoder.Decode(&request); err != nil {
		return ChangePlanRequest{}, fmt.Errorf("%w: decode: %v", ErrInvalidChangePlan, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ChangePlanRequest{}, fmt.Errorf("%w: decode: %v", ErrInvalidChangePlan, err)
	}
	return request, nil
}

// BuildChangePlan validates and canonicalizes a fail-closed network change
// plan. The returned object is deterministic and never executes its steps.
func BuildChangePlan(request ChangePlanRequest) (ChangePlan, error) {
	nodeID, err := normalizeIdentifier("node_id", request.NodeID)
	if err != nil {
		return ChangePlan{}, err
	}
	revisionID, err := normalizeIdentifier("revision_id", request.RevisionID)
	if err != nil {
		return ChangePlan{}, err
	}
	if err := validateTimeouts(request.Timeouts); err != nil {
		return ChangePlan{}, err
	}

	interfaces, interfaceByID, err := canonicalInterfaces(request.Interfaces)
	if err != nil {
		return ChangePlan{}, err
	}
	forwarding, err := canonicalForwarding(request.Forwarding, interfaceByID)
	if err != nil {
		return ChangePlan{}, err
	}
	probes, err := canonicalProbes(request.Probes, interfaceByID)
	if err != nil {
		return ChangePlan{}, err
	}
	if err := validateProbeCoverage(interfaces, probes); err != nil {
		return ChangePlan{}, err
	}

	steps := []ChangeStep{
		{Order: 1, Stage: ChangeStageSnapshot, Action: ActionCaptureRecoverySnapshot},
		{Order: 2, Stage: ChangeStagePreflight, Action: ActionValidateNetworkContract},
		{Order: 3, Stage: ChangeStagePreflight, Action: ActionValidateManagementPath},
		{Order: 4, Stage: ChangeStagePreflight, Action: ActionAuthorizeForwarding},
		{Order: 5, Stage: ChangeStageApplyWindow, Action: ActionOpenTemporaryWindow},
		{Order: 6, Stage: ChangeStageProbes, Action: ActionRunConnectivityProbes},
		{Order: 7, Stage: ChangeStageDecision, Action: ActionCommitOrRollback},
	}
	plan := ChangePlan{
		SchemaVersion:         ChangePlanSchemaVersion,
		NodeID:                nodeID,
		RevisionID:            revisionID,
		Interfaces:            interfaces,
		Forwarding:            forwarding,
		ForwardingDefaultDeny: true,
		Probes:                probes,
		Timeouts:              request.Timeouts,
		Steps:                 steps,
	}
	plan.PlanID = changePlanID(plan)
	return plan, nil
}

func canonicalInterfaces(input []InterfaceIntent) ([]InterfaceIntent, map[string]InterfaceIntent, error) {
	if len(input) == 0 || len(input) > maxInterfacesPerPlan {
		return nil, nil, fmt.Errorf("%w: interfaces must contain 1..%d entries", ErrInvalidChangePlan, maxInterfacesPerPlan)
	}
	result := make([]InterfaceIntent, 0, len(input))
	byID := make(map[string]InterfaceIntent, len(input))
	changed := 0
	for _, item := range input {
		id, err := normalizeIdentifier("interface_id", item.InterfaceID)
		if err != nil {
			return nil, nil, err
		}
		item.InterfaceID = id
		item.Zone = Zone(strings.ToUpper(strings.TrimSpace(string(item.Zone))))
		if !item.Zone.Valid() {
			return nil, nil, fmt.Errorf("%w: interface %q has invalid zone %q", ErrInvalidChangePlan, id, item.Zone)
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate interface %q", ErrInvalidChangePlan, id)
		}
		if item.Changed {
			changed++
		}
		byID[id] = item
		result = append(result, item)
	}
	if changed == 0 {
		return nil, nil, fmt.Errorf("%w: at least one interface must be marked changed", ErrInvalidChangePlan)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].InterfaceID < result[j].InterfaceID })
	return result, byID, nil
}

func canonicalForwarding(input []ForwardingIntent, interfaces map[string]InterfaceIntent) ([]ForwardingIntent, error) {
	if len(input) > maxForwardingIntents {
		return nil, fmt.Errorf("%w: forwarding exceeds %d entries", ErrInvalidChangePlan, maxForwardingIntents)
	}
	result := append([]ForwardingIntent(nil), input...)
	seen := make(map[string]struct{}, len(result))
	zones := make(map[Zone]struct{}, len(interfaces))
	for _, networkInterface := range interfaces {
		zones[networkInterface.Zone] = struct{}{}
	}
	for index := range result {
		result[index].Source = Zone(strings.ToUpper(strings.TrimSpace(string(result[index].Source))))
		result[index].Destination = Zone(strings.ToUpper(strings.TrimSpace(string(result[index].Destination))))
		if err := AuthorizeForwarding(result[index]); err != nil {
			return nil, fmt.Errorf("%w: forwarding %s to %s: %v", ErrInvalidChangePlan, result[index].Source, result[index].Destination, err)
		}
		if _, exists := zones[result[index].Source]; !exists {
			return nil, fmt.Errorf("%w: forwarding source zone %s is not declared by an interface", ErrInvalidChangePlan, result[index].Source)
		}
		if _, exists := zones[result[index].Destination]; !exists {
			return nil, fmt.Errorf("%w: forwarding destination zone %s is not declared by an interface", ErrInvalidChangePlan, result[index].Destination)
		}
		key := string(result[index].Source) + "\x00" + string(result[index].Destination)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate forwarding intent %s to %s", ErrInvalidChangePlan, result[index].Source, result[index].Destination)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source == result[j].Source {
			return result[i].Destination < result[j].Destination
		}
		return result[i].Source < result[j].Source
	})
	return result, nil
}

func canonicalProbes(input []ConnectivityProbe, interfaces map[string]InterfaceIntent) ([]ConnectivityProbe, error) {
	if len(input) == 0 || len(input) > maxProbesPerPlan {
		return nil, fmt.Errorf("%w: probes must contain 1..%d entries", ErrInvalidChangePlan, maxProbesPerPlan)
	}
	result := make([]ConnectivityProbe, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, probe := range input {
		id, err := normalizeIdentifier("probe.id", probe.ID)
		if err != nil {
			return nil, err
		}
		interfaceID, err := normalizeIdentifier("probe.interface_id", probe.InterfaceID)
		if err != nil {
			return nil, err
		}
		probe.ID = id
		probe.InterfaceID = interfaceID
		probe.Zone = Zone(strings.ToUpper(strings.TrimSpace(string(probe.Zone))))
		if !probe.Kind.valid() {
			return nil, fmt.Errorf("%w: probe %q has invalid kind %q", ErrInvalidChangePlan, id, probe.Kind)
		}
		bound, exists := interfaces[interfaceID]
		if !exists || bound.Zone != probe.Zone {
			return nil, fmt.Errorf("%w: probe %q interface/zone binding is not declared", ErrInvalidChangePlan, id)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: duplicate probe %q", ErrInvalidChangePlan, id)
		}
		seen[id] = struct{}{}
		result = append(result, probe)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func validateProbeCoverage(interfaces []InterfaceIntent, probes []ConnectivityProbe) error {
	covered := make(map[string]bool, len(interfaces))
	managementPath := false
	for _, probe := range probes {
		covered[probe.InterfaceID] = true
		if probe.Kind == ProbeControlPlane && (probe.Zone == ZoneManagement || probe.Zone == ZoneLAN) {
			managementPath = true
		}
	}
	for _, networkInterface := range interfaces {
		if networkInterface.Changed && !covered[networkInterface.InterfaceID] {
			return fmt.Errorf("%w: changed interface %q has no connectivity probe", ErrInvalidChangePlan, networkInterface.InterfaceID)
		}
	}
	if !managementPath {
		return fmt.Errorf("%w: a control_plane probe bound to MANAGEMENT or LAN is required", ErrInvalidChangePlan)
	}
	return nil
}

func validateTimeouts(timeouts TimeoutPolicy) error {
	values := []struct {
		name  string
		value time.Duration
	}{
		{name: "snapshot", value: timeouts.Snapshot},
		{name: "preflight", value: timeouts.Preflight},
		{name: "apply_window", value: timeouts.ApplyWindow},
		{name: "probe", value: timeouts.Probe},
		{name: "rollback", value: timeouts.Rollback},
	}
	for _, value := range values {
		if value.value <= 0 || value.value > maxPhaseTimeout {
			return fmt.Errorf("%w: timeout %s must be greater than zero and at most %s", ErrInvalidChangePlan, value.name, maxPhaseTimeout)
		}
	}
	if timeouts.Probe > timeouts.ApplyWindow {
		return fmt.Errorf("%w: probe timeout must not exceed apply window", ErrInvalidChangePlan)
	}
	return nil
}

func normalizeIdentifier(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !identifierPattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s is invalid", ErrInvalidChangePlan, name)
	}
	return value, nil
}

func changePlanID(plan ChangePlan) string {
	plan.PlanID = ""
	document, err := json.Marshal(plan)
	if err != nil {
		panic("network change plan contains only JSON-safe values: " + err.Error())
	}
	digest := sha256.Sum256(document)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values are not allowed")
	}
	return err
}
