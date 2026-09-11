// Package nodelifecycle defines the side-effect-free node lifecycle contract.
// Persistence, authorization, orchestration and evidence provenance belong to
// adapters that call this package before an atomic compare-and-swap write.
package nodelifecycle

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"control-center/internal/corecontracts"
)

const maxReasonLength = 512

var (
	// ErrInvalidLifecycle identifies a malformed lifecycle object.
	ErrInvalidLifecycle = errors.New("invalid node lifecycle")
	// ErrInvalidEvidence identifies missing, duplicate or unknown transition
	// evidence. Evidence provenance must still be verified by the caller.
	ErrInvalidEvidence = errors.New("invalid node lifecycle transition evidence")
)

// State is the durable lifecycle state of one physical Control Center node.
// Ready is the contract name for the ACTIVE architecture state after enrollment
// and readiness checks have passed.
type State string

const (
	StateDiscovered  State = "discovered"
	StateEnrolling   State = "enrolling"
	StateReady       State = "ready"
	StateDegraded    State = "degraded"
	StateDraining    State = "draining"
	StateMaintenance State = "maintenance"
	StateUpdating    State = "updating"
	StateReplacing   State = "replacing"
	StateRemoving    State = "removing"
	StateOffline     State = "offline"
	StateRecovering  State = "recovering"
	StateRetired     State = "retired"
)

// Valid reports whether the state is part of this version of the contract.
func (s State) Valid() bool {
	switch s {
	case StateDiscovered,
		StateEnrolling,
		StateReady,
		StateDegraded,
		StateDraining,
		StateMaintenance,
		StateUpdating,
		StateReplacing,
		StateRemoving,
		StateOffline,
		StateRecovering,
		StateRetired:
		return true
	default:
		return false
	}
}

// Terminal reports whether the state has no valid successors.
func (s State) Terminal() bool { return s == StateRetired }

// TransitionType determines the distributed object generation semantics.
// Desired transitions represent operator intent and increment Generation.
// Observation transitions report progress or health and preserve Generation.
type TransitionType string

const (
	TransitionDesired     TransitionType = "desired"
	TransitionObservation TransitionType = "observation"
)

func (t TransitionType) valid() bool {
	return t == TransitionDesired || t == TransitionObservation
}

// EvidenceCheck is a typed attestation required by safety-sensitive edges.
// The lifecycle contract validates the set; the caller verifies that trusted
// subsystems produced the attestations before invoking ValidateTransition.
type EvidenceCheck string

const (
	CheckEnrollmentAuthorized       EvidenceCheck = "enrollment-authorized"
	CheckIdentityVerified           EvidenceCheck = "identity-verified"
	CheckReadinessPassed            EvidenceCheck = "readiness-passed"
	CheckMaintenancePreflightPassed EvidenceCheck = "maintenance-preflight-passed"
	CheckSchedulingDisabled         EvidenceCheck = "scheduling-disabled"
	CheckSchedulingEnabled          EvidenceCheck = "scheduling-enabled"
	CheckDrainComplete              EvidenceCheck = "drain-complete"
	CheckNoActiveJobs               EvidenceCheck = "no-active-jobs"
	CheckNoActivePlacements         EvidenceCheck = "no-active-placements"
	CheckStatefulWorkloadsSafe      EvidenceCheck = "stateful-workloads-safe"
	CheckOperationApproved          EvidenceCheck = "operation-approved"
	CheckOperationCancelled         EvidenceCheck = "operation-cancelled"
	CheckRollbackVerified           EvidenceCheck = "rollback-verified"
	CheckUpdateVerified             EvidenceCheck = "update-verified"
	CheckReplacementNodeReady       EvidenceCheck = "replacement-node-ready"
	CheckStateSynchronized          EvidenceCheck = "state-synchronized"
	CheckSwitchoverVerified         EvidenceCheck = "switchover-verified"
	CheckReplacementHealthVerified  EvidenceCheck = "replacement-health-verified"
	CheckRemovalApproved            EvidenceCheck = "removal-approved"
	CheckRemovalVerified            EvidenceCheck = "removal-verified"
	CheckRecoveryStarted            EvidenceCheck = "recovery-started"
	CheckRecoveryVerified           EvidenceCheck = "recovery-verified"
	CheckRetirementApproved         EvidenceCheck = "retirement-approved"
	CheckNoManagedState             EvidenceCheck = "no-managed-state"
)

func (c EvidenceCheck) valid() bool {
	switch c {
	case CheckEnrollmentAuthorized,
		CheckIdentityVerified,
		CheckReadinessPassed,
		CheckMaintenancePreflightPassed,
		CheckSchedulingDisabled,
		CheckSchedulingEnabled,
		CheckDrainComplete,
		CheckNoActiveJobs,
		CheckNoActivePlacements,
		CheckStatefulWorkloadsSafe,
		CheckOperationApproved,
		CheckOperationCancelled,
		CheckRollbackVerified,
		CheckUpdateVerified,
		CheckReplacementNodeReady,
		CheckStateSynchronized,
		CheckSwitchoverVerified,
		CheckReplacementHealthVerified,
		CheckRemovalApproved,
		CheckRemovalVerified,
		CheckRecoveryStarted,
		CheckRecoveryVerified,
		CheckRetirementApproved,
		CheckNoManagedState:
		return true
	default:
		return false
	}
}

// TransitionEvidence contains only checks that have passed. Unknown and
// duplicate checks are rejected so an unsupported proof cannot be mistaken for
// a supported safety gate.
type TransitionEvidence struct {
	PassedChecks []EvidenceCheck `json:"passed_checks,omitempty"`
}

// NodeLifecycle is the versioned lifecycle object for one node. ObjectID is
// the stable Node ID; StateChangedAt is the time the current state was entered.
type NodeLifecycle struct {
	corecontracts.ObjectMetadata
	State          State     `json:"state"`
	StateChangedAt time.Time `json:"state_changed_at"`
	Reason         string    `json:"reason,omitempty"`
}

// TransitionRequest is the caller-supplied portion of a lifecycle change. The
// storage adapter allocates the next ResourceVersion and performs this
// validation atomically with its compare-and-swap write.
type TransitionRequest struct {
	To           State                            `json:"to"`
	Type         TransitionType                   `json:"type"`
	Reason       string                           `json:"reason,omitempty"`
	Precondition corecontracts.ObjectPrecondition `json:"precondition"`
	Evidence     TransitionEvidence               `json:"evidence"`
}

type transitionRule struct {
	typeOf         TransitionType
	requiredChecks []EvidenceCheck
}

var transitionRules = map[State]map[State]transitionRule{
	StateDiscovered: {
		StateEnrolling: {typeOf: TransitionDesired, requiredChecks: checks(CheckEnrollmentAuthorized)},
		StateRetired:   {typeOf: TransitionDesired, requiredChecks: checks(CheckRetirementApproved, CheckNoManagedState)},
	},
	StateEnrolling: {
		StateReady:    {typeOf: TransitionObservation, requiredChecks: checks(CheckIdentityVerified, CheckReadinessPassed)},
		StateDegraded: {typeOf: TransitionObservation},
		StateOffline:  {typeOf: TransitionObservation},
		StateRetired:  {typeOf: TransitionDesired, requiredChecks: checks(CheckRetirementApproved, CheckNoManagedState)},
	},
	StateReady: {
		StateDegraded: {typeOf: TransitionObservation},
		StateDraining: {typeOf: TransitionDesired, requiredChecks: checks(CheckMaintenancePreflightPassed, CheckSchedulingDisabled)},
		StateOffline:  {typeOf: TransitionObservation},
	},
	StateDegraded: {
		StateReady:      {typeOf: TransitionObservation, requiredChecks: checks(CheckReadinessPassed)},
		StateDraining:   {typeOf: TransitionDesired, requiredChecks: checks(CheckMaintenancePreflightPassed, CheckSchedulingDisabled)},
		StateOffline:    {typeOf: TransitionObservation},
		StateRecovering: {typeOf: TransitionObservation, requiredChecks: checks(CheckRecoveryStarted)},
	},
	StateDraining: {
		StateMaintenance: {typeOf: TransitionObservation, requiredChecks: drainChecks()},
		StateReady:       {typeOf: TransitionDesired, requiredChecks: checks(CheckOperationCancelled, CheckSchedulingEnabled, CheckReadinessPassed)},
		StateDegraded:    {typeOf: TransitionObservation},
		StateOffline:     {typeOf: TransitionObservation},
	},
	StateMaintenance: {
		StateReady:     {typeOf: TransitionDesired, requiredChecks: checks(CheckSchedulingEnabled, CheckReadinessPassed)},
		StateUpdating:  {typeOf: TransitionDesired, requiredChecks: checks(CheckOperationApproved, CheckMaintenancePreflightPassed)},
		StateReplacing: {typeOf: TransitionDesired, requiredChecks: checks(CheckOperationApproved, CheckReplacementNodeReady, CheckStateSynchronized)},
		StateRemoving:  {typeOf: TransitionDesired, requiredChecks: append(checks(CheckOperationApproved, CheckRemovalApproved), drainChecks()...)},
		StateDegraded:  {typeOf: TransitionObservation},
		StateOffline:   {typeOf: TransitionObservation},
	},
	StateUpdating: {
		StateReady:       {typeOf: TransitionObservation, requiredChecks: checks(CheckUpdateVerified, CheckSchedulingEnabled, CheckReadinessPassed)},
		StateMaintenance: {typeOf: TransitionObservation, requiredChecks: checks(CheckUpdateVerified)},
		StateDegraded:    {typeOf: TransitionObservation},
		StateOffline:     {typeOf: TransitionObservation},
	},
	StateReplacing: {
		StateRetired:     {typeOf: TransitionObservation, requiredChecks: checks(CheckReplacementNodeReady, CheckStateSynchronized, CheckSwitchoverVerified, CheckReplacementHealthVerified)},
		StateMaintenance: {typeOf: TransitionDesired, requiredChecks: checks(CheckOperationCancelled, CheckRollbackVerified)},
		StateDegraded:    {typeOf: TransitionObservation},
		StateOffline:     {typeOf: TransitionObservation},
	},
	StateRemoving: {
		StateRetired:     {typeOf: TransitionObservation, requiredChecks: checks(CheckRemovalVerified, CheckRetirementApproved)},
		StateMaintenance: {typeOf: TransitionDesired, requiredChecks: checks(CheckOperationCancelled, CheckRollbackVerified)},
		StateDegraded:    {typeOf: TransitionObservation},
		StateOffline:     {typeOf: TransitionObservation},
	},
	StateOffline: {
		StateRecovering: {typeOf: TransitionObservation, requiredChecks: checks(CheckIdentityVerified, CheckRecoveryStarted)},
		StateRetired:    {typeOf: TransitionDesired, requiredChecks: checks(CheckRetirementApproved, CheckNoManagedState, CheckStatefulWorkloadsSafe)},
	},
	StateRecovering: {
		StateReady:    {typeOf: TransitionObservation, requiredChecks: checks(CheckRecoveryVerified, CheckReadinessPassed)},
		StateDegraded: {typeOf: TransitionObservation},
		StateOffline:  {typeOf: TransitionObservation},
		StateDraining: {typeOf: TransitionDesired, requiredChecks: checks(CheckMaintenancePreflightPassed, CheckSchedulingDisabled)},
	},
	StateRetired: {},
}

func checks(values ...EvidenceCheck) []EvidenceCheck { return values }

func drainChecks() []EvidenceCheck {
	return checks(
		CheckMaintenancePreflightPassed,
		CheckSchedulingDisabled,
		CheckDrainComplete,
		CheckNoActiveJobs,
		CheckNoActivePlacements,
		CheckStatefulWorkloadsSafe,
	)
}

// Validate validates a stored lifecycle object independently of its history.
func Validate(lifecycle NodeLifecycle) error {
	if err := lifecycle.ObjectMetadata.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLifecycle, err)
	}
	if !lifecycle.State.Valid() {
		return fmt.Errorf("%w: unsupported state %q", ErrInvalidLifecycle, lifecycle.State)
	}
	if lifecycle.StateChangedAt.IsZero() {
		return fmt.Errorf("%w: state_changed_at is required", ErrInvalidLifecycle)
	}
	if lifecycle.StateChangedAt.Before(lifecycle.CreatedAt) {
		return fmt.Errorf("%w: state_changed_at precedes created_at", ErrInvalidLifecycle)
	}
	if lifecycle.StateChangedAt.After(lifecycle.UpdatedAt) {
		return fmt.Errorf("%w: state_changed_at follows updated_at", ErrInvalidLifecycle)
	}
	if err := validateReason(lifecycle.State, lifecycle.Reason); err != nil {
		return err
	}
	return nil
}

// ValidateInitial enforces the only valid starting point for a new lifecycle
// object. Discovery itself is an observation, so the initial generation is one.
func ValidateInitial(lifecycle NodeLifecycle) error {
	if err := Validate(lifecycle); err != nil {
		return err
	}
	if lifecycle.State != StateDiscovered {
		return fmt.Errorf("%w: initial state must be %q", ErrInvalidLifecycle, StateDiscovered)
	}
	if lifecycle.Generation != 1 {
		return fmt.Errorf("%w: initial generation must be 1", ErrInvalidLifecycle)
	}
	if !lifecycle.CreatedAt.Equal(lifecycle.UpdatedAt) || !lifecycle.StateChangedAt.Equal(lifecycle.CreatedAt) {
		return fmt.Errorf("%w: initial timestamps must be equal", ErrInvalidLifecycle)
	}
	return nil
}

// ValidateTransition validates a complete state change without mutating either
// object. A storage adapter must run it in the same transaction/CAS operation
// that writes next; a prior stand-alone call is not a concurrency guarantee.
func ValidateTransition(current, next NodeLifecycle, request TransitionRequest) error {
	if err := Validate(current); err != nil {
		return fmt.Errorf("current: %w", err)
	}
	if err := Validate(next); err != nil {
		return fmt.Errorf("next: %w", err)
	}
	if !request.To.Valid() {
		return fmt.Errorf("%w: unsupported target state %q", corecontracts.ErrInvalidTransition, request.To)
	}
	if !request.Type.valid() {
		return fmt.Errorf("%w: unsupported transition type %q", corecontracts.ErrInvalidTransition, request.Type)
	}
	if request.To != next.State {
		return fmt.Errorf("%w: request target %q differs from next state %q", corecontracts.ErrInvalidTransition, request.To, next.State)
	}
	if request.Reason != next.Reason {
		return fmt.Errorf("%w: request reason differs from the stored successor", corecontracts.ErrInvalidTransition)
	}
	if err := request.Precondition.ValidateAgainst(current.ObjectMetadata); err != nil {
		return fmt.Errorf("node lifecycle precondition: %w", err)
	}

	rule, allowed := transitionRuleFor(current.State, next.State)
	if !allowed {
		return fmt.Errorf("%w: node lifecycle %s -> %s", corecontracts.ErrInvalidTransition, current.State, next.State)
	}
	if request.Type != rule.typeOf {
		return fmt.Errorf("%w: %s -> %s requires transition type %q", corecontracts.ErrInvalidTransition, current.State, next.State, rule.typeOf)
	}
	if current.ScopeID != next.ScopeID || current.OwnerScope != next.OwnerScope {
		return fmt.Errorf("%w: lifecycle transition cannot change scope ownership", corecontracts.ErrInvalidTransition)
	}
	if !next.StateChangedAt.Equal(next.UpdatedAt) {
		return fmt.Errorf("%w: successor state_changed_at must equal updated_at", corecontracts.ErrInvalidTransition)
	}
	if !next.StateChangedAt.After(current.StateChangedAt) {
		return fmt.Errorf("%w: successor state_changed_at must advance", corecontracts.ErrInvalidTransition)
	}
	if err := request.Evidence.validate(rule.requiredChecks); err != nil {
		return err
	}
	desiredChanged := request.Type == TransitionDesired
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, desiredChanged); err != nil {
		return err
	}
	return nil
}

func transitionRuleFor(from, to State) (transitionRule, bool) {
	toRules, exists := transitionRules[from]
	if !exists {
		return transitionRule{}, false
	}
	rule, exists := toRules[to]
	return rule, exists
}

func (e TransitionEvidence) validate(required []EvidenceCheck) error {
	passed := make(map[EvidenceCheck]struct{}, len(e.PassedChecks))
	for _, check := range e.PassedChecks {
		if !check.valid() {
			return fmt.Errorf("%w: unsupported check %q", ErrInvalidEvidence, check)
		}
		if _, duplicate := passed[check]; duplicate {
			return fmt.Errorf("%w: duplicate check %q", ErrInvalidEvidence, check)
		}
		passed[check] = struct{}{}
	}
	for _, check := range required {
		if _, exists := passed[check]; !exists {
			return fmt.Errorf("%w: required check %q did not pass", ErrInvalidEvidence, check)
		}
	}
	return nil
}

func validateReason(state State, reason string) error {
	if strings.TrimSpace(reason) != reason {
		return fmt.Errorf("%w: reason must not have surrounding whitespace", ErrInvalidLifecycle)
	}
	if !utf8.ValidString(reason) {
		return fmt.Errorf("%w: reason is not valid UTF-8", ErrInvalidLifecycle)
	}
	if utf8.RuneCountInString(reason) > maxReasonLength {
		return fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidLifecycle, maxReasonLength)
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: reason contains control characters", ErrInvalidLifecycle)
		}
	}
	reasonRequired := state == StateDegraded || state == StateOffline || state == StateRecovering || state == StateRetired
	if reasonRequired && reason == "" {
		return fmt.Errorf("%w: state %q requires a reason", ErrInvalidLifecycle, state)
	}
	if !reasonRequired && reason != "" {
		return fmt.Errorf("%w: state %q must clear the incident reason", ErrInvalidLifecycle, state)
	}
	return nil
}
