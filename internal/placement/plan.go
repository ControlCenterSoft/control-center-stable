// Package placement builds deterministic, side-effect-free workload placement plans.
package placement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"control-center/internal/corecontracts"
)

const PlanContractV1 = "placement.plan/v1"

var ErrInvalidPlan = errors.New("invalid placement plan")

type WorkloadClass string

const (
	WorkloadStateless WorkloadClass = "stateless"
	WorkloadStateful  WorkloadClass = "stateful"
)

type Resources struct {
	CPUMillicores uint64 `json:"cpu_millicores"`
	MemoryMiB     uint64 `json:"memory_mib"`
	StorageGiB    uint64 `json:"storage_gib"`
}

type Candidate struct {
	NodeID      string                   `json:"node_id"`
	SiteID      string                   `json:"site_id"`
	ZoneID      string                   `json:"zone_id"`
	Roles       []corecontracts.NodeRole `json:"roles"`
	Healthy     bool                     `json:"healthy"`
	Schedulable bool                     `json:"schedulable"`
	Allocatable Resources                `json:"allocatable"`
	Used        Resources                `json:"used"`
}

type Request struct {
	WorkloadID                 string                 `json:"workload_id"`
	Class                      WorkloadClass          `json:"class"`
	RequiredRole               corecontracts.NodeRole `json:"required_role"`
	AllowedSites               []string               `json:"allowed_sites,omitempty"`
	AllowedZones               []string               `json:"allowed_zones,omitempty"`
	AntiAffinityNodeIDs        []string               `json:"anti_affinity_node_ids,omitempty"`
	Resources                  Resources              `json:"resources"`
	CurrentNodeID              string                 `json:"current_node_id,omitempty"`
	MigrationAdapter           string                 `json:"migration_adapter,omitempty"`
	HealthyReplicasOutsideNode uint32                 `json:"healthy_replicas_outside_node"`
	TopologyGeneration         uint64                 `json:"topology_generation"`
	TopologyResourceVersion    string                 `json:"topology_resource_version"`
}

type Plan struct {
	ContractVersion         string    `json:"contract_version"`
	PlanID                  string    `json:"plan_id"`
	WorkloadID              string    `json:"workload_id"`
	SelectedNodeID          string    `json:"selected_node_id"`
	SelectedSiteID          string    `json:"selected_site_id"`
	SelectedZoneID          string    `json:"selected_zone_id"`
	Resources               Resources `json:"resources"`
	DominantUtilizationPPM  uint64    `json:"dominant_utilization_ppm"`
	TopologyGeneration      uint64    `json:"topology_generation"`
	TopologyResourceVersion string    `json:"topology_resource_version"`
	RequiresApprovedChange  bool      `json:"requires_approved_change"`
	RequiresDurableJob      bool      `json:"requires_durable_job"`
	RequiresAudit           bool      `json:"requires_audit"`
	RevalidateBeforeExecute bool      `json:"revalidate_before_execute"`
	PlanOnly                bool      `json:"plan_only"`
	HostMutation            bool      `json:"host_mutation"`
}

// BuildPlan selects the least-loaded eligible node. It does not reserve resources or mutate a host.
func BuildPlan(request Request, candidates []Candidate) (Plan, error) {
	request, err := normalizeRequest(request)
	if err != nil {
		return Plan{}, err
	}
	if len(candidates) == 0 || len(candidates) > 4096 {
		return Plan{}, invalid("candidates must contain between 1 and 4096 nodes")
	}

	type ranked struct {
		candidate Candidate
		score     uint64
	}
	eligible := make([]ranked, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, raw := range candidates {
		candidate, normalizeErr := normalizeCandidate(raw)
		if normalizeErr != nil {
			return Plan{}, normalizeErr
		}
		if _, duplicate := seen[candidate.NodeID]; duplicate {
			return Plan{}, invalid("duplicate candidate %q", candidate.NodeID)
		}
		seen[candidate.NodeID] = struct{}{}
		if !candidate.Healthy || !candidate.Schedulable || !containsRole(candidate.Roles, request.RequiredRole) ||
			!allowed(candidate.SiteID, request.AllowedSites) || !allowed(candidate.ZoneID, request.AllowedZones) ||
			contains(request.AntiAffinityNodeIDs, candidate.NodeID) || !fits(candidate, request.Resources) {
			continue
		}
		eligible = append(eligible, ranked{candidate: candidate, score: dominantPPM(candidate, request.Resources)})
	}
	if len(eligible) == 0 {
		return Plan{}, invalid("no healthy schedulable candidate satisfies placement constraints")
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].score != eligible[j].score {
			return eligible[i].score < eligible[j].score
		}
		return eligible[i].candidate.NodeID < eligible[j].candidate.NodeID
	})
	selected := eligible[0]
	if request.Class == WorkloadStateful && request.CurrentNodeID != "" && selected.candidate.NodeID != request.CurrentNodeID {
		if request.MigrationAdapter == "" || request.HealthyReplicasOutsideNode == 0 {
			return Plan{}, invalid("stateful move requires a migration adapter and a healthy replica outside the current node")
		}
	}

	fingerprint := struct {
		Request  Request   `json:"request"`
		Selected Candidate `json:"selected"`
		Score    uint64    `json:"score"`
	}{request, selected.candidate, selected.score}
	encoded, marshalErr := json.Marshal(fingerprint)
	if marshalErr != nil {
		return Plan{}, invalid("fingerprint: %v", marshalErr)
	}
	digest := sha256.Sum256(encoded)
	return Plan{
		ContractVersion: PlanContractV1, PlanID: "plc-" + hex.EncodeToString(digest[:])[:24],
		WorkloadID: request.WorkloadID, SelectedNodeID: selected.candidate.NodeID,
		SelectedSiteID: selected.candidate.SiteID, SelectedZoneID: selected.candidate.ZoneID,
		Resources: request.Resources, DominantUtilizationPPM: selected.score,
		TopologyGeneration: request.TopologyGeneration, TopologyResourceVersion: request.TopologyResourceVersion,
		RequiresApprovedChange: true, RequiresDurableJob: true, RequiresAudit: true,
		RevalidateBeforeExecute: true, PlanOnly: true, HostMutation: false,
	}, nil
}

func normalizeRequest(request Request) (Request, error) {
	request.WorkloadID = strings.TrimSpace(request.WorkloadID)
	request.CurrentNodeID = strings.TrimSpace(request.CurrentNodeID)
	request.MigrationAdapter = strings.TrimSpace(request.MigrationAdapter)
	request.TopologyResourceVersion = strings.TrimSpace(request.TopologyResourceVersion)
	if err := identifier("workload_id", request.WorkloadID); err != nil {
		return Request{}, err
	}
	if request.Class != WorkloadStateless && request.Class != WorkloadStateful {
		return Request{}, invalid("unsupported workload class %q", request.Class)
	}
	if !request.RequiredRole.Valid() {
		return Request{}, invalid("required_role is not canonical")
	}
	if request.Resources.CPUMillicores == 0 || request.Resources.MemoryMiB == 0 || request.Resources.StorageGiB == 0 {
		return Request{}, invalid("resource requirements must be positive")
	}
	if request.TopologyGeneration == 0 {
		return Request{}, invalid("topology_generation is required")
	}
	if err := identifier("topology_resource_version", request.TopologyResourceVersion); err != nil {
		return Request{}, err
	}
	var err error
	if request.AllowedSites, err = canonicalIDs("allowed_sites", request.AllowedSites); err != nil {
		return Request{}, err
	}
	if request.AllowedZones, err = canonicalIDs("allowed_zones", request.AllowedZones); err != nil {
		return Request{}, err
	}
	if request.AntiAffinityNodeIDs, err = canonicalIDs("anti_affinity_node_ids", request.AntiAffinityNodeIDs); err != nil {
		return Request{}, err
	}
	if request.CurrentNodeID != "" {
		if err := identifier("current_node_id", request.CurrentNodeID); err != nil {
			return Request{}, err
		}
	}
	if request.MigrationAdapter != "" {
		if err := identifier("migration_adapter", request.MigrationAdapter); err != nil {
			return Request{}, err
		}
	}
	return request, nil
}

func normalizeCandidate(candidate Candidate) (Candidate, error) {
	candidate.NodeID, candidate.SiteID, candidate.ZoneID = strings.TrimSpace(candidate.NodeID), strings.TrimSpace(candidate.SiteID), strings.TrimSpace(candidate.ZoneID)
	for field, value := range map[string]string{"node_id": candidate.NodeID, "site_id": candidate.SiteID, "zone_id": candidate.ZoneID} {
		if err := identifier(field, value); err != nil {
			return Candidate{}, err
		}
	}
	if candidate.Allocatable.CPUMillicores == 0 || candidate.Allocatable.MemoryMiB == 0 || candidate.Allocatable.StorageGiB == 0 {
		return Candidate{}, invalid("candidate %q has zero allocatable resource", candidate.NodeID)
	}
	if candidate.Used.CPUMillicores > candidate.Allocatable.CPUMillicores || candidate.Used.MemoryMiB > candidate.Allocatable.MemoryMiB || candidate.Used.StorageGiB > candidate.Allocatable.StorageGiB {
		return Candidate{}, invalid("candidate %q usage exceeds allocatable resources", candidate.NodeID)
	}
	candidate.Roles = append([]corecontracts.NodeRole(nil), candidate.Roles...)
	if len(candidate.Roles) == 0 {
		return Candidate{}, invalid("candidate %q has no roles", candidate.NodeID)
	}
	for _, role := range candidate.Roles {
		if !role.Valid() {
			return Candidate{}, invalid("candidate %q has invalid role", candidate.NodeID)
		}
	}
	sort.Slice(candidate.Roles, func(i, j int) bool { return candidate.Roles[i] < candidate.Roles[j] })
	for i := 1; i < len(candidate.Roles); i++ {
		if candidate.Roles[i] == candidate.Roles[i-1] {
			return Candidate{}, invalid("candidate %q repeats a role", candidate.NodeID)
		}
	}
	return candidate, nil
}

func fits(candidate Candidate, need Resources) bool {
	return need.CPUMillicores <= candidate.Allocatable.CPUMillicores-candidate.Used.CPUMillicores && need.MemoryMiB <= candidate.Allocatable.MemoryMiB-candidate.Used.MemoryMiB && need.StorageGiB <= candidate.Allocatable.StorageGiB-candidate.Used.StorageGiB
}
func dominantPPM(candidate Candidate, need Resources) uint64 {
	values := []uint64{(candidate.Used.CPUMillicores + need.CPUMillicores) * 1_000_000 / candidate.Allocatable.CPUMillicores, (candidate.Used.MemoryMiB + need.MemoryMiB) * 1_000_000 / candidate.Allocatable.MemoryMiB, (candidate.Used.StorageGiB + need.StorageGiB) * 1_000_000 / candidate.Allocatable.StorageGiB}
	sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
	return values[0]
}
func containsRole(values []corecontracts.NodeRole, wanted corecontracts.NodeRole) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func contains(values []string, wanted string) bool {
	i := sort.SearchStrings(values, wanted)
	return i < len(values) && values[i] == wanted
}
func allowed(value string, allowedValues []string) bool {
	return len(allowedValues) == 0 || contains(allowedValues, value)
}
func canonicalIDs(field string, values []string) ([]string, error) {
	result := append([]string(nil), values...)
	for i := range result {
		result[i] = strings.TrimSpace(result[i])
		if err := identifier(field, result[i]); err != nil {
			return nil, err
		}
	}
	sort.Strings(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, invalid("%s contains duplicate %q", field, result[i])
		}
	}
	return result, nil
}
func identifier(field, value string) error {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return invalid("%s is required and must be canonical", field)
	}
	for i, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !ok && r != '.' && r != '_' && r != ':' && r != '-' {
			return invalid("%s is not a canonical identifier", field)
		}
		if (i == 0 || i == len(value)-1) && !ok {
			return invalid("%s must have alphanumeric ends", field)
		}
	}
	return nil
}
func invalid(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPlan, fmt.Sprintf(format, values...))
}
