package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

func (r Risk) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

type ApprovalRequirement struct {
	Minimum           int    `json:"minimum"`
	Permission        string `json:"permission,omitempty"`
	DistinctActors    bool   `json:"distinctActors"`
	ProhibitRequester bool   `json:"prohibitRequester"`
}

func (r ApprovalRequirement) Validate() error {
	if r.Minimum < 0 {
		return errors.New("minimum approvals cannot be negative")
	}
	if r.Permission != strings.TrimSpace(r.Permission) {
		return errors.New("approval permission must be canonical")
	}
	if r.Minimum > 0 && r.Permission == "" {
		return errors.New("approval permission is required")
	}
	return nil
}

type Decision struct {
	Effect      Effect              `json:"effect"`
	Risk        Risk                `json:"risk"`
	Reason      string              `json:"reason"`
	Requirement ApprovalRequirement `json:"requirement"`
	PolicyID    string              `json:"policyId"`
}

func (d Decision) Validate() error {
	if d.Effect != EffectAllow && d.Effect != EffectDeny {
		return errors.New("invalid policy effect")
	}
	if !d.Risk.Valid() {
		return errors.New("invalid risk")
	}
	if d.PolicyID == "" || d.Reason == "" {
		return errors.New("policy id and reason are required")
	}
	return d.Requirement.Validate()
}

type EvaluationInput struct {
	Action      string
	Requester   string
	Risk        Risk
	Permissions []string
}
type Evaluator interface {
	Evaluate(EvaluationInput) (Decision, error)
}
type ThresholdEvaluator struct {
	PolicyID           string
	ApprovalPermission string
	DeniedActions      map[string]string
}

func (e ThresholdEvaluator) Evaluate(in EvaluationInput) (Decision, error) {
	if in.Action == "" || in.Requester == "" || !in.Risk.Valid() {
		return Decision{}, errors.New("action, requester, and valid risk are required")
	}
	if reason, denied := e.DeniedActions[in.Action]; denied {
		return Decision{Effect: EffectDeny, Risk: in.Risk, Reason: reason, PolicyID: e.PolicyID}, nil
	}
	required := 0
	switch in.Risk {
	case RiskHigh:
		required = 1
	case RiskCritical:
		required = 2
	}
	decision := Decision{Effect: EffectAllow, Risk: in.Risk, Reason: "risk threshold policy satisfied", PolicyID: e.PolicyID, Requirement: ApprovalRequirement{Minimum: required, Permission: e.ApprovalPermission, DistinctActors: true, ProhibitRequester: required > 0}}
	return decision, decision.Validate()
}

type Approval struct {
	Actor       string    `json:"actor"`
	Permissions []string  `json:"permissions"`
	ApprovedAt  time.Time `json:"approvedAt"`
}

func CheckApprovals(requester string, requirement ApprovalRequirement, approvals []Approval) error {
	if err := requirement.Validate(); err != nil {
		return err
	}
	if requirement.ProhibitRequester {
		trimmedRequester := strings.TrimSpace(requester)
		if trimmedRequester == "" || requester != trimmedRequester {
			return errors.New("canonical requester is required for requester-prohibited approval policy")
		}
	}
	seen := make(map[string]struct{}, len(approvals))
	valid := 0
	for _, approval := range approvals {
		trimmedActor := strings.TrimSpace(approval.Actor)
		if trimmedActor == "" || approval.Actor != trimmedActor || approval.ApprovedAt.IsZero() {
			continue
		}
		if requirement.ProhibitRequester && approval.Actor == requester {
			continue
		}
		if requirement.DistinctActors {
			if _, duplicate := seen[approval.Actor]; duplicate {
				continue
			}
			seen[approval.Actor] = struct{}{}
		}
		if requirement.Permission != "" && !contains(approval.Permissions, requirement.Permission) {
			continue
		}
		valid++
	}
	if valid < requirement.Minimum {
		return fmt.Errorf("insufficient approvals: have %d, require %d", valid, requirement.Minimum)
	}
	return nil
}
func contains(values []string, wanted string) bool {
	copy := sortedCopy(values)
	i := sort.SearchStrings(copy, wanted)
	return i < len(copy) && copy[i] == wanted
}
func sortedCopy(values []string) []string {
	copy := append([]string(nil), values...)
	sort.Strings(copy)
	return copy
}
