package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

const LifecyclePlanSchemaVersion = "domain.lifecycle.plan/v1"

var (
	ErrInvalidLifecyclePlan = errors.New("invalid domain lifecycle plan")
	domainLabelPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	nodeIDPattern           = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)
)

// LifecycleOperation is a supported directory lifecycle transition. The
// health-preflight operation is read-only; all other operations describe a
// desired mutation but do not execute it.
type LifecycleOperation string

const (
	LifecycleCreate          LifecycleOperation = "create"
	LifecycleJoin            LifecycleOperation = "join"
	LifecyclePromote         LifecycleOperation = "promote"
	LifecycleHealthPreflight LifecycleOperation = "health-preflight"
)

// LifecycleStage separates prerequisite checks, provider changes and
// post-change verification without exposing an arbitrary command surface.
type LifecycleStage string

const (
	LifecycleStagePreflight LifecycleStage = "preflight"
	LifecycleStageApply     LifecycleStage = "apply"
	LifecycleStageVerify    LifecycleStage = "verify"
)

// LifecycleAction is a closed set of actions understood by provider adapters.
// It deliberately carries no shell command, endpoint or credential material.
type LifecycleAction string

const (
	ActionValidateProviderCompatibility LifecycleAction = "validate-provider-compatibility"
	ActionValidateDNSReadiness          LifecycleAction = "validate-dns-readiness"
	ActionValidateTimeSyncReadiness     LifecycleAction = "validate-time-sync-readiness"
	ActionValidateStorageReadiness      LifecycleAction = "validate-storage-readiness"
	ActionDiscoverDirectory             LifecycleAction = "discover-directory"

	ActionSambaProvisionDomain   LifecycleAction = "samba-ad-dc.provision-domain"
	ActionSambaJoinMember        LifecycleAction = "samba-ad-dc.join-member"
	ActionSambaPromoteController LifecycleAction = "samba-ad-dc.promote-controller"
	ActionSambaCheckHealth       LifecycleAction = "samba-ad-dc.check-directory-health"
	ActionSambaVerifyMembership  LifecycleAction = "samba-ad-dc.verify-membership"
	ActionSambaVerifyReplication LifecycleAction = "samba-ad-dc.verify-replication"

	ActionFreeIPAProvisionDomain   LifecycleAction = "freeipa.provision-domain"
	ActionFreeIPAEnrollHost        LifecycleAction = "freeipa.enroll-host"
	ActionFreeIPAPromoteReplica    LifecycleAction = "freeipa.promote-replica"
	ActionFreeIPACheckHealth       LifecycleAction = "freeipa.check-directory-health"
	ActionFreeIPAVerifyMembership  LifecycleAction = "freeipa.verify-membership"
	ActionFreeIPAVerifyReplication LifecycleAction = "freeipa.verify-replication"
)

type LifecycleTarget struct {
	NodeID   string `json:"node_id"`
	Platform string `json:"platform"`
}

type LifecyclePlanRequest struct {
	Provider     Provider           `json:"provider"`
	Operation    LifecycleOperation `json:"operation"`
	DomainName   string             `json:"domain_name"`
	Target       LifecycleTarget    `json:"target"`
	Requirements Requirements       `json:"requirements"`
}

type LifecycleStep struct {
	Order  int             `json:"order"`
	Stage  LifecycleStage  `json:"stage"`
	Action LifecycleAction `json:"action"`
}

type LifecyclePlan struct {
	SchemaVersion string             `json:"schema_version"`
	PlanID        string             `json:"plan_id"`
	Provider      Provider           `json:"provider"`
	Operation     LifecycleOperation `json:"operation"`
	DomainName    string             `json:"domain_name"`
	Target        LifecycleTarget    `json:"target"`
	Requirements  Requirements       `json:"requirements"`
	Mutating      bool               `json:"mutating"`
	Steps         []LifecycleStep    `json:"steps"`
}

// BuildLifecyclePlan returns a canonical, deterministic plan. Provider choice
// must be explicit: automatic provider selection is intentionally limited to
// ResolveProvider and is never performed for a lifecycle operation.
func BuildLifecyclePlan(request LifecyclePlanRequest) (LifecyclePlan, error) {
	provider, ok := canonicalProvider(request.Provider)
	if !ok || provider == ProviderAuto {
		return LifecyclePlan{}, fmt.Errorf("%w: an explicit supported provider is required", ErrInvalidLifecyclePlan)
	}
	if _, err := ResolveProvider(provider, request.Requirements); err != nil {
		return LifecyclePlan{}, fmt.Errorf("%w: %w", ErrInvalidLifecyclePlan, err)
	}

	operation := LifecycleOperation(strings.ToLower(strings.TrimSpace(string(request.Operation))))
	if !validLifecycleOperation(operation) {
		return LifecyclePlan{}, fmt.Errorf("%w: unsupported operation %q", ErrInvalidLifecyclePlan, request.Operation)
	}

	domainName, err := normalizeLifecycleDomainName(request.DomainName)
	if err != nil {
		return LifecyclePlan{}, err
	}
	target, err := normalizeLifecycleTarget(request.Target)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if operation != LifecycleJoin && target.Platform != "linux" {
		return LifecyclePlan{}, fmt.Errorf("%w: %s requires a linux provider node", ErrInvalidLifecyclePlan, operation)
	}
	if operation == LifecycleJoin {
		if err := ValidateDirectoryJoin(DirectoryJoinRequest{
			Platform:   target.Platform,
			Provider:   string(provider),
			DomainName: domainName,
		}); err != nil {
			return LifecyclePlan{}, fmt.Errorf("%w: %v", ErrInvalidLifecyclePlan, err)
		}
	}

	steps := lifecycleSteps(provider, operation)
	plan := LifecyclePlan{
		SchemaVersion: LifecyclePlanSchemaVersion,
		Provider:      provider,
		Operation:     operation,
		DomainName:    domainName,
		Target:        target,
		Requirements:  request.Requirements,
		Mutating:      operation != LifecycleHealthPreflight,
		Steps:         steps,
	}
	plan.PlanID = lifecyclePlanID(plan)
	return plan, nil
}

func validLifecycleOperation(operation LifecycleOperation) bool {
	switch operation {
	case LifecycleCreate, LifecycleJoin, LifecyclePromote, LifecycleHealthPreflight:
		return true
	default:
		return false
	}
}

func normalizeLifecycleDomainName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil {
		return "", fmt.Errorf("%w: domain_name must be a DNS domain", ErrInvalidLifecyclePlan)
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%w: domain_name must contain at least two DNS labels", ErrInvalidLifecyclePlan)
	}
	for _, label := range labels {
		if !domainLabelPattern.MatchString(label) {
			return "", fmt.Errorf("%w: domain_name contains an invalid DNS label", ErrInvalidLifecyclePlan)
		}
	}
	return value, nil
}

func normalizeLifecycleTarget(target LifecycleTarget) (LifecycleTarget, error) {
	target.NodeID = strings.TrimSpace(target.NodeID)
	target.Platform = strings.ToLower(strings.TrimSpace(target.Platform))
	if !nodeIDPattern.MatchString(target.NodeID) {
		return LifecycleTarget{}, fmt.Errorf("%w: target.node_id is invalid", ErrInvalidLifecyclePlan)
	}
	if target.Platform != "linux" && target.Platform != "windows" {
		return LifecycleTarget{}, fmt.Errorf("%w: target.platform must be linux or windows", ErrInvalidLifecyclePlan)
	}
	return target, nil
}

func lifecycleSteps(provider Provider, operation LifecycleOperation) []LifecycleStep {
	steps := []LifecycleStep{
		{Stage: LifecycleStagePreflight, Action: ActionValidateProviderCompatibility},
		{Stage: LifecycleStagePreflight, Action: ActionValidateDNSReadiness},
		{Stage: LifecycleStagePreflight, Action: ActionValidateTimeSyncReadiness},
	}

	switch operation {
	case LifecycleCreate:
		steps = append(steps,
			LifecycleStep{Stage: LifecycleStagePreflight, Action: ActionValidateStorageReadiness},
			LifecycleStep{Stage: LifecycleStageApply, Action: providerProvisionAction(provider)},
			LifecycleStep{Stage: LifecycleStageVerify, Action: providerHealthAction(provider)},
		)
	case LifecycleJoin:
		steps = append(steps,
			LifecycleStep{Stage: LifecycleStagePreflight, Action: ActionDiscoverDirectory},
			LifecycleStep{Stage: LifecycleStageApply, Action: providerJoinAction(provider)},
			LifecycleStep{Stage: LifecycleStageVerify, Action: providerMembershipAction(provider)},
		)
	case LifecyclePromote:
		steps = append(steps,
			LifecycleStep{Stage: LifecycleStagePreflight, Action: ActionValidateStorageReadiness},
			LifecycleStep{Stage: LifecycleStagePreflight, Action: ActionDiscoverDirectory},
			LifecycleStep{Stage: LifecycleStageApply, Action: providerPromoteAction(provider)},
			LifecycleStep{Stage: LifecycleStageVerify, Action: providerReplicationAction(provider)},
		)
	case LifecycleHealthPreflight:
		steps = append(steps,
			LifecycleStep{Stage: LifecycleStagePreflight, Action: ActionValidateStorageReadiness},
			LifecycleStep{Stage: LifecycleStagePreflight, Action: ActionDiscoverDirectory},
			LifecycleStep{Stage: LifecycleStagePreflight, Action: providerHealthAction(provider)},
			LifecycleStep{Stage: LifecycleStagePreflight, Action: providerReplicationAction(provider)},
		)
	}
	for index := range steps {
		steps[index].Order = index + 1
	}
	return steps
}

func providerProvisionAction(provider Provider) LifecycleAction {
	if provider == ProviderSamba {
		return ActionSambaProvisionDomain
	}
	return ActionFreeIPAProvisionDomain
}

func providerJoinAction(provider Provider) LifecycleAction {
	if provider == ProviderSamba {
		return ActionSambaJoinMember
	}
	return ActionFreeIPAEnrollHost
}

func providerPromoteAction(provider Provider) LifecycleAction {
	if provider == ProviderSamba {
		return ActionSambaPromoteController
	}
	return ActionFreeIPAPromoteReplica
}

func providerHealthAction(provider Provider) LifecycleAction {
	if provider == ProviderSamba {
		return ActionSambaCheckHealth
	}
	return ActionFreeIPACheckHealth
}

func providerMembershipAction(provider Provider) LifecycleAction {
	if provider == ProviderSamba {
		return ActionSambaVerifyMembership
	}
	return ActionFreeIPAVerifyMembership
}

func providerReplicationAction(provider Provider) LifecycleAction {
	if provider == ProviderSamba {
		return ActionSambaVerifyReplication
	}
	return ActionFreeIPAVerifyReplication
}

func lifecyclePlanID(plan LifecyclePlan) string {
	var canonical strings.Builder
	fmt.Fprintf(&canonical, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%t\x00%t",
		plan.SchemaVersion,
		plan.Provider,
		plan.Operation,
		plan.DomainName,
		plan.Target.NodeID,
		plan.Target.Platform,
		plan.Requirements.WindowsDomainJoin,
		plan.Requirements.GroupPolicy,
		plan.Mutating,
	)
	for _, step := range plan.Steps {
		fmt.Fprintf(&canonical, "\x00%d\x00%s\x00%s", step.Order, step.Stage, step.Action)
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return "sha256:" + hex.EncodeToString(digest[:])
}
