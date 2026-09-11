package nodelifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"control-center/internal/corecontracts"
)

var ErrInvalidPlan = errors.New("invalid node lifecycle transition plan")

// TransitionPlan is proof that a transition request is valid against one
// projection version. It is not an execution record and cannot mutate state.
type TransitionPlan struct {
	PlanID                   string          `json:"plan_id"`
	Accepted                 bool            `json:"accepted"`
	PlanOnly                 bool            `json:"plan_only"`
	HostMutation             bool            `json:"host_mutation"`
	StateMutation            bool            `json:"state_mutation"`
	RequiresAuditedChangeJob bool            `json:"requires_audited_change_job"`
	NodeID                   string          `json:"node_id"`
	From                     State           `json:"from"`
	To                       State           `json:"to"`
	Type                     TransitionType  `json:"type"`
	CurrentGeneration        uint64          `json:"current_generation"`
	PlannedGeneration        uint64          `json:"planned_generation"`
	BasedOnResourceVersion   string          `json:"based_on_resource_version"`
	RequiredChecks           []EvidenceCheck `json:"required_checks"`
	EvaluatedAt              time.Time       `json:"evaluated_at"`
}

// BuildTransitionPlan validates a request using a synthetic successor that is
// never returned or stored. A later executor must re-read state and repeat the
// validation atomically inside the audited Change/Job persistence boundary.
func BuildTransitionPlan(current NodeLifecycle, request TransitionRequest, evaluatedAt time.Time) (TransitionPlan, error) {
	if err := Validate(current); err != nil {
		return TransitionPlan{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if evaluatedAt.IsZero() {
		return TransitionPlan{}, fmt.Errorf("%w: evaluated_at is required", ErrInvalidPlan)
	}
	evaluatedAt = evaluatedAt.UTC()
	if !evaluatedAt.After(current.UpdatedAt) {
		return TransitionPlan{}, fmt.Errorf("%w: evaluated_at must follow the projected object update", ErrInvalidPlan)
	}

	fingerprint, err := transitionFingerprint(current, request)
	if err != nil {
		return TransitionPlan{}, err
	}
	next := current
	next.State = request.To
	next.Reason = request.Reason
	next.UpdatedAt = evaluatedAt
	next.StateChangedAt = evaluatedAt
	next.ResourceVersion = "plan:" + fingerprint
	if request.Type == TransitionDesired {
		if current.Generation == math.MaxUint64 {
			return TransitionPlan{}, fmt.Errorf("%w: generation overflow", corecontracts.ErrInvalidTransition)
		}
		next.Generation++
	}
	if err := ValidateTransition(current, next, request); err != nil {
		return TransitionPlan{}, err
	}

	rule, exists := transitionRuleFor(current.State, request.To)
	if !exists {
		return TransitionPlan{}, fmt.Errorf("%w: missing rule after validation", ErrInvalidPlan)
	}
	requiredChecks := make([]EvidenceCheck, len(rule.requiredChecks))
	copy(requiredChecks, rule.requiredChecks)
	return TransitionPlan{
		PlanID:                   "nlp-" + fingerprint[:24],
		Accepted:                 true,
		PlanOnly:                 true,
		HostMutation:             false,
		StateMutation:            false,
		RequiresAuditedChangeJob: true,
		NodeID:                   current.ObjectID,
		From:                     current.State,
		To:                       request.To,
		Type:                     request.Type,
		CurrentGeneration:        current.Generation,
		PlannedGeneration:        next.Generation,
		BasedOnResourceVersion:   current.ResourceVersion,
		RequiredChecks:           requiredChecks,
		EvaluatedAt:              evaluatedAt,
	}, nil
}

func transitionFingerprint(current NodeLifecycle, request TransitionRequest) (string, error) {
	passedChecks := append([]EvidenceCheck(nil), request.Evidence.PassedChecks...)
	sort.Slice(passedChecks, func(i, j int) bool { return passedChecks[i] < passedChecks[j] })
	fingerprintInput := struct {
		NodeID          string                           `json:"node_id"`
		ResourceVersion string                           `json:"resource_version"`
		Generation      uint64                           `json:"generation"`
		From            State                            `json:"from"`
		To              State                            `json:"to"`
		Type            TransitionType                   `json:"type"`
		Reason          string                           `json:"reason"`
		Precondition    corecontracts.ObjectPrecondition `json:"precondition"`
		PassedChecks    []EvidenceCheck                  `json:"passed_checks"`
	}{
		NodeID:          current.ObjectID,
		ResourceVersion: current.ResourceVersion,
		Generation:      current.Generation,
		From:            current.State,
		To:              request.To,
		Type:            request.Type,
		Reason:          request.Reason,
		Precondition:    request.Precondition,
		PassedChecks:    passedChecks,
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return "", fmt.Errorf("%w: fingerprint: %v", ErrInvalidPlan, err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
