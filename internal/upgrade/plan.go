// Package upgrade defines side-effect-free multi-node upgrade orchestration.
package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/corecontracts"
)

const PlanContractV1 = "upgrade.orchestrator-plan/v1"

var ErrInvalidPlan = errors.New("invalid upgrade orchestrator plan")

type Target struct {
	NodeID         string                 `json:"node_id"`
	Role           corecontracts.NodeRole `json:"role"`
	CurrentVersion string                 `json:"current_version"`
	TargetVersion  string                 `json:"target_version"`
	Ring           int                    `json:"ring"`
	Dependencies   []string               `json:"dependencies,omitempty"`
}

type MaintenanceWindow struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type Request struct {
	Targets                   []Target          `json:"targets"`
	CanaryNodeID              string            `json:"canary_node_id"`
	MaxParallel               int               `json:"max_parallel"`
	ControllerPopulation      int               `json:"controller_population"`
	MinimumHealthyControllers int               `json:"minimum_healthy_controllers"`
	Window                    MaintenanceWindow `json:"maintenance_window"`
}

type Wave struct {
	Order   int      `json:"order"`
	Ring    int      `json:"ring"`
	NodeIDs []string `json:"node_ids"`
	Canary  bool     `json:"canary"`
}

// Plan is an immutable rollout instruction. An executor must re-check the
// maintenance window, health, versions and quorum before every wave.
type Plan struct {
	ContractVersion           string            `json:"contract_version"`
	PlanID                    string            `json:"plan_id"`
	Window                    MaintenanceWindow `json:"maintenance_window"`
	Waves                     []Wave            `json:"waves"`
	CanaryFailureStopsRollout bool              `json:"canary_failure_stops_rollout"`
	RollbackRequired          bool              `json:"rollback_required"`
	HealthCheckBeforeNextWave bool              `json:"health_check_before_next_wave"`
	RequiresApprovedChange    bool              `json:"requires_approved_change"`
	RequiresDurableJobs       bool              `json:"requires_durable_jobs"`
	RequiresAudit             bool              `json:"requires_audit"`
	PlanOnly                  bool              `json:"plan_only"`
	HostMutation              bool              `json:"host_mutation"`
}

func BuildPlan(request Request) (Plan, error) {
	if request.MaxParallel < 1 || request.MaxParallel > 64 {
		return Plan{}, invalid("max_parallel must be between 1 and 64")
	}
	if request.Window.StartsAt.IsZero() || request.Window.EndsAt.IsZero() ||
		!request.Window.EndsAt.After(request.Window.StartsAt) {
		return Plan{}, invalid("maintenance window is invalid")
	}
	duration := request.Window.EndsAt.Sub(request.Window.StartsAt)
	if duration < 5*time.Minute || duration > 7*24*time.Hour {
		return Plan{}, invalid("maintenance window must be between five minutes and seven days")
	}
	if request.ControllerPopulation < 0 || request.MinimumHealthyControllers < 0 ||
		request.MinimumHealthyControllers > request.ControllerPopulation {
		return Plan{}, invalid("controller quorum boundary is invalid")
	}
	canary := strings.TrimSpace(request.CanaryNodeID)
	if err := validateIdentifier("canary_node_id", canary); err != nil {
		return Plan{}, err
	}
	if len(request.Targets) == 0 || len(request.Targets) > 1024 {
		return Plan{}, invalid("targets must contain between 1 and 1024 nodes")
	}

	targets := make(map[string]Target, len(request.Targets))
	controllerTargets := 0
	for _, candidate := range request.Targets {
		target, err := normalizeTarget(candidate)
		if err != nil {
			return Plan{}, err
		}
		if _, exists := targets[target.NodeID]; exists {
			return Plan{}, invalid("duplicate target %q", target.NodeID)
		}
		targets[target.NodeID] = target
		if controllerRole(target.Role) {
			controllerTargets++
		}
	}
	if controllerTargets > request.ControllerPopulation {
		return Plan{}, invalid("controller targets exceed controller population")
	}
	canaryTarget, exists := targets[canary]
	if !exists {
		return Plan{}, invalid("canary node is not an upgrade target")
	}
	if len(canaryTarget.Dependencies) != 0 {
		return Plan{}, invalid("canary must not depend on another upgrade target")
	}
	if !wavePreservesControllers(
		[]Target{canaryTarget},
		request.ControllerPopulation,
		request.MinimumHealthyControllers,
	) {
		return Plan{}, invalid("controller health boundary prevents the canary wave")
	}

	for _, target := range targets {
		for _, dependencyID := range target.Dependencies {
			dependency, exists := targets[dependencyID]
			if !exists {
				return Plan{}, invalid("target %q has unknown dependency %q", target.NodeID, dependencyID)
			}
			if dependency.Ring > target.Ring {
				return Plan{}, invalid("target %q depends on later ring %q", target.NodeID, dependencyID)
			}
		}
	}

	waves := []Wave{{Order: 1, Ring: canaryTarget.Ring, NodeIDs: []string{canary}, Canary: true}}
	done := map[string]bool{canary: true}
	for len(done) < len(targets) {
		eligible := make([]Target, 0)
		minimumRing := int(^uint(0) >> 1)
		for id, target := range targets {
			if done[id] || !dependenciesComplete(target.Dependencies, done) {
				continue
			}
			if target.Ring < minimumRing {
				minimumRing = target.Ring
				eligible = eligible[:0]
			}
			if target.Ring == minimumRing {
				eligible = append(eligible, target)
			}
		}
		if len(eligible) == 0 {
			return Plan{}, invalid("upgrade dependency graph contains a cycle")
		}
		sort.Slice(eligible, func(i, j int) bool {
			leftController := controllerRole(eligible[i].Role)
			rightController := controllerRole(eligible[j].Role)
			if leftController != rightController {
				return !leftController
			}
			return eligible[i].NodeID < eligible[j].NodeID
		})
		for len(eligible) > 0 {
			limit := request.MaxParallel
			if limit > len(eligible) {
				limit = len(eligible)
			}
			batch := append([]Target(nil), eligible[:limit]...)
			for !wavePreservesControllers(batch, request.ControllerPopulation, request.MinimumHealthyControllers) {
				limit--
				if limit == 0 {
					return Plan{}, invalid("controller health boundary prevents a safe rollout wave")
				}
				batch = append([]Target(nil), eligible[:limit]...)
			}
			nodeIDs := make([]string, len(batch))
			for index, target := range batch {
				nodeIDs[index] = target.NodeID
				done[target.NodeID] = true
			}
			waves = append(waves, Wave{Order: len(waves) + 1, Ring: minimumRing, NodeIDs: nodeIDs})
			eligible = eligible[limit:]
		}
	}

	canonicalTargets := make([]Target, 0, len(targets))
	for _, target := range targets {
		canonicalTargets = append(canonicalTargets, target)
	}
	sort.Slice(canonicalTargets, func(i, j int) bool { return canonicalTargets[i].NodeID < canonicalTargets[j].NodeID })
	fingerprintInput := struct {
		Targets                   []Target          `json:"targets"`
		CanaryNodeID              string            `json:"canary_node_id"`
		MaxParallel               int               `json:"max_parallel"`
		ControllerPopulation      int               `json:"controller_population"`
		MinimumHealthyControllers int               `json:"minimum_healthy_controllers"`
		Window                    MaintenanceWindow `json:"maintenance_window"`
	}{
		Targets: canonicalTargets, CanaryNodeID: canary, MaxParallel: request.MaxParallel,
		ControllerPopulation:      request.ControllerPopulation,
		MinimumHealthyControllers: request.MinimumHealthyControllers,
		Window:                    MaintenanceWindow{StartsAt: request.Window.StartsAt.UTC(), EndsAt: request.Window.EndsAt.UTC()},
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return Plan{}, invalid("fingerprint: %v", err)
	}
	digest := sha256.Sum256(encoded)
	return Plan{
		ContractVersion: PlanContractV1, PlanID: "uop-" + hex.EncodeToString(digest[:])[:24],
		Window: fingerprintInput.Window, Waves: waves,
		CanaryFailureStopsRollout: true, RollbackRequired: true, HealthCheckBeforeNextWave: true,
		RequiresApprovedChange: true, RequiresDurableJobs: true, RequiresAudit: true, PlanOnly: true,
	}, nil
}

func normalizeTarget(target Target) (Target, error) {
	target.NodeID = strings.TrimSpace(target.NodeID)
	target.CurrentVersion = strings.TrimSpace(target.CurrentVersion)
	target.TargetVersion = strings.TrimSpace(target.TargetVersion)
	target.Dependencies = append([]string(nil), target.Dependencies...)
	if err := validateIdentifier("node_id", target.NodeID); err != nil {
		return Target{}, err
	}
	if !target.Role.Valid() {
		return Target{}, invalid("node %q has unsupported role %q", target.NodeID, target.Role)
	}
	if err := validateIdentifier("current_version", target.CurrentVersion); err != nil {
		return Target{}, err
	}
	if err := validateIdentifier("target_version", target.TargetVersion); err != nil {
		return Target{}, err
	}
	if target.CurrentVersion == target.TargetVersion {
		return Target{}, invalid("node %q is already at target version", target.NodeID)
	}
	if target.Ring < 0 || target.Ring > 9 {
		return Target{}, invalid("node %q has invalid update ring", target.NodeID)
	}
	seen := make(map[string]struct{}, len(target.Dependencies))
	for index, value := range target.Dependencies {
		value = strings.TrimSpace(value)
		if err := validateIdentifier("dependency", value); err != nil {
			return Target{}, err
		}
		if value == target.NodeID {
			return Target{}, invalid("node %q depends on itself", target.NodeID)
		}
		if _, exists := seen[value]; exists {
			return Target{}, invalid("node %q repeats dependency %q", target.NodeID, value)
		}
		seen[value] = struct{}{}
		target.Dependencies[index] = value
	}
	sort.Strings(target.Dependencies)
	return target, nil
}

func dependenciesComplete(dependencies []string, done map[string]bool) bool {
	for _, dependency := range dependencies {
		if !done[dependency] {
			return false
		}
	}
	return true
}

func wavePreservesControllers(targets []Target, population, minimumHealthy int) bool {
	controllers := 0
	for _, target := range targets {
		if controllerRole(target.Role) {
			controllers++
		}
	}
	return population-controllers >= minimumHealthy
}

func controllerRole(role corecontracts.NodeRole) bool {
	return role == corecontracts.RoleGlobalController ||
		role == corecontracts.RoleSiteController ||
		role == corecontracts.RoleControllerClusterMember
}

func validateIdentifier(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return invalid("%s is required and must be canonical", field)
	}
	for index, character := range value {
		alphanumeric := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9'
		if !alphanumeric && character != '.' && character != '_' && character != ':' && character != '-' {
			return invalid("%s is not a canonical identifier", field)
		}
		if (index == 0 || index == len(value)-1) && !alphanumeric {
			return invalid("%s must have alphanumeric ends", field)
		}
	}
	return nil
}

func invalid(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPlan, fmt.Sprintf(format, values...))
}
