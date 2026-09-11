package nodelifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const OperationPlanContractV1 = "node.operation-plan/v1"

var ErrInvalidOperationPlan = errors.New("invalid node operation plan")

type OperationKind string

const (
	OperationDrain   OperationKind = "drain"
	OperationReplace OperationKind = "replace"
)

type WorkloadKind string

const (
	WorkloadStateless WorkloadKind = "stateless"
	WorkloadStateful  WorkloadKind = "stateful"
)

// WorkloadPlacement is the safety-relevant subset of a placement snapshot.
// Stateful movement is admitted only with an explicit provider adapter and a
// healthy replica outside the node being drained or replaced.
type WorkloadPlacement struct {
	WorkloadID             string       `json:"workload_id"`
	Kind                   WorkloadKind `json:"kind"`
	MigrationAdapter       string       `json:"migration_adapter,omitempty"`
	HealthyReplicasOutside int          `json:"healthy_replicas_outside"`
}

type OperationPlanRequest struct {
	Kind              OperationKind       `json:"kind"`
	ReplacementNodeID string              `json:"replacement_node_id,omitempty"`
	Placements        []WorkloadPlacement `json:"placements,omitempty"`
}

type OperationPlanStep struct {
	Order            int             `json:"order"`
	Action           string          `json:"action"`
	RequiredEvidence []EvidenceCheck `json:"required_evidence,omitempty"`
}

// OperationPlan coordinates multiple lifecycle edges without applying any of
// them. Each mutating step must later be admitted by an approved Change,
// durable Job and Audit event against a fresh lifecycle projection.
type OperationPlan struct {
	ContractVersion        string              `json:"contract_version"`
	PlanID                 string              `json:"plan_id"`
	Kind                   OperationKind       `json:"kind"`
	NodeID                 string              `json:"node_id"`
	ReplacementNodeID      string              `json:"replacement_node_id,omitempty"`
	BasedOnResourceVersion string              `json:"based_on_resource_version"`
	Steps                  []OperationPlanStep `json:"steps"`
	RequiresApprovedChange bool                `json:"requires_approved_change"`
	RequiresDurableJob     bool                `json:"requires_durable_job"`
	RequiresAudit          bool                `json:"requires_audit"`
	PlanOnly               bool                `json:"plan_only"`
	LifecycleMutation      bool                `json:"lifecycle_mutation"`
	PlacementMutation      bool                `json:"placement_mutation"`
	HostMutation           bool                `json:"host_mutation"`
}

func BuildOperationPlan(current NodeLifecycle, request OperationPlanRequest) (OperationPlan, error) {
	if err := Validate(current); err != nil {
		return OperationPlan{}, fmt.Errorf("%w: lifecycle: %v", ErrInvalidOperationPlan, err)
	}
	placements := append([]WorkloadPlacement(nil), request.Placements...)
	if err := validatePlacements(placements); err != nil {
		return OperationPlan{}, err
	}
	sort.Slice(placements, func(i, j int) bool { return placements[i].WorkloadID < placements[j].WorkloadID })

	var steps []OperationPlanStep
	switch request.Kind {
	case OperationDrain:
		if current.State != StateReady && current.State != StateDegraded {
			return OperationPlan{}, invalidOperation("drain requires ready or degraded state")
		}
		if strings.TrimSpace(request.ReplacementNodeID) != "" {
			return OperationPlan{}, invalidOperation("drain cannot name a replacement node")
		}
		steps = []OperationPlanStep{
			{Order: 1, Action: "scheduler.disable", RequiredEvidence: []EvidenceCheck{CheckMaintenancePreflightPassed}},
			{Order: 2, Action: "lifecycle.enter-draining", RequiredEvidence: []EvidenceCheck{CheckMaintenancePreflightPassed, CheckSchedulingDisabled}},
			{Order: 3, Action: "placements.evacuate"},
			{Order: 4, Action: "lifecycle.enter-maintenance", RequiredEvidence: drainChecks()},
		}
	case OperationReplace:
		if current.State != StateMaintenance {
			return OperationPlan{}, invalidOperation("replace requires maintenance state after a completed drain")
		}
		replacement := strings.TrimSpace(request.ReplacementNodeID)
		if err := validateOperationIdentifier("replacement_node_id", replacement); err != nil {
			return OperationPlan{}, err
		}
		if replacement == current.ObjectID {
			return OperationPlan{}, invalidOperation("replacement node must differ from current node")
		}
		request.ReplacementNodeID = replacement
		steps = []OperationPlanStep{
			{Order: 1, Action: "replacement.verify-ready", RequiredEvidence: []EvidenceCheck{CheckOperationApproved}},
			{Order: 2, Action: "state.synchronize", RequiredEvidence: []EvidenceCheck{CheckReplacementNodeReady}},
			{Order: 3, Action: "lifecycle.enter-replacing", RequiredEvidence: []EvidenceCheck{CheckOperationApproved, CheckReplacementNodeReady, CheckStateSynchronized}},
			{Order: 4, Action: "placements.switchover", RequiredEvidence: []EvidenceCheck{CheckStateSynchronized}},
			{Order: 5, Action: "replacement.verify-health", RequiredEvidence: []EvidenceCheck{CheckSwitchoverVerified}},
			{Order: 6, Action: "lifecycle.retire-replaced-node", RequiredEvidence: []EvidenceCheck{CheckReplacementNodeReady, CheckStateSynchronized, CheckSwitchoverVerified, CheckReplacementHealthVerified}},
		}
	default:
		return OperationPlan{}, invalidOperation("unsupported operation kind %q", request.Kind)
	}

	fingerprintInput := struct {
		Kind              OperationKind       `json:"kind"`
		NodeID            string              `json:"node_id"`
		ResourceVersion   string              `json:"resource_version"`
		ReplacementNodeID string              `json:"replacement_node_id,omitempty"`
		Placements        []WorkloadPlacement `json:"placements,omitempty"`
	}{
		Kind: request.Kind, NodeID: current.ObjectID, ResourceVersion: current.ResourceVersion,
		ReplacementNodeID: request.ReplacementNodeID, Placements: placements,
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return OperationPlan{}, invalidOperation("fingerprint: %v", err)
	}
	digest := sha256.Sum256(encoded)
	return OperationPlan{
		ContractVersion: OperationPlanContractV1,
		PlanID:          "nop-" + hex.EncodeToString(digest[:])[:24],
		Kind:            request.Kind, NodeID: current.ObjectID,
		ReplacementNodeID:      request.ReplacementNodeID,
		BasedOnResourceVersion: current.ResourceVersion,
		Steps:                  steps,
		RequiresApprovedChange: true, RequiresDurableJob: true, RequiresAudit: true,
		PlanOnly: true,
	}, nil
}

func validatePlacements(placements []WorkloadPlacement) error {
	seen := make(map[string]struct{}, len(placements))
	for _, placement := range placements {
		if err := validateOperationIdentifier("workload_id", placement.WorkloadID); err != nil {
			return err
		}
		if _, exists := seen[placement.WorkloadID]; exists {
			return invalidOperation("duplicate workload %q", placement.WorkloadID)
		}
		seen[placement.WorkloadID] = struct{}{}
		switch placement.Kind {
		case WorkloadStateless:
			if placement.HealthyReplicasOutside < 0 {
				return invalidOperation("workload %q has a negative replica count", placement.WorkloadID)
			}
		case WorkloadStateful:
			if err := validateOperationIdentifier("migration_adapter", placement.MigrationAdapter); err != nil {
				return invalidOperation("stateful workload %q requires a migration adapter", placement.WorkloadID)
			}
			if placement.HealthyReplicasOutside < 1 {
				return invalidOperation("stateful workload %q has no healthy replica outside the node", placement.WorkloadID)
			}
		default:
			return invalidOperation("workload %q has unsupported kind %q", placement.WorkloadID, placement.Kind)
		}
	}
	return nil
}

func validateOperationIdentifier(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return invalidOperation("%s is required and must be canonical", field)
	}
	for index, character := range value {
		valid := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == ':' || character == '-'
		if !valid || (index == 0 || index == len(value)-1) && !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9') {
			return invalidOperation("%s is not a canonical identifier", field)
		}
	}
	return nil
}

func invalidOperation(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOperationPlan, fmt.Sprintf(format, values...))
}
