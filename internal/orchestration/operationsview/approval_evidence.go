package operationsview

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/policy"
)

const (
	ApprovalEvidenceContractVersion     = "ui.operations-approval-evidence/v1"
	MaxApprovalEvidenceRecords          = 256
	MaxApprovalEvidenceIdentifierLength = 255
)

var ErrInvalidApprovalEvidence = errors.New("invalid operations approval evidence")

type ApprovalEvidenceState string

const (
	ApprovalEvidenceDenied      ApprovalEvidenceState = "denied"
	ApprovalEvidenceNotRequired ApprovalEvidenceState = "not_required"
	ApprovalEvidencePending     ApprovalEvidenceState = "pending"
	ApprovalEvidenceSatisfied   ApprovalEvidenceState = "satisfied"
)

// ApprovalEvidenceRecord intentionally excludes the approver permission set.
// The UI needs actor/time evidence for an accepted approval, not a copy of the
// actor's authorization material.
type ApprovalEvidenceRecord struct {
	Actor      string    `json:"actor"`
	ApprovedAt time.Time `json:"approved_at"`
}

// ApprovalEvidence is a bounded, read-only projection of the approval policy
// and accepted approval records for one exact immutable Change revision.
// Presence of this evidence never authorizes execution or changes Change state.
type ApprovalEvidence struct {
	ContractVersion    string                   `json:"contract_version"`
	ChangeID           string                   `json:"change_id"`
	RevisionID         string                   `json:"revision_id"`
	RevisionDigest     string                   `json:"revision_digest"`
	PolicyID           string                   `json:"policy_id"`
	Risk               policy.Risk              `json:"risk"`
	State              ApprovalEvidenceState    `json:"state"`
	RequiredCount      int                      `json:"required_count"`
	RecordedCount      int                      `json:"recorded_count"`
	EligibleCount      int                      `json:"eligible_count"`
	Satisfied          bool                     `json:"satisfied"`
	DistinctActors     bool                     `json:"distinct_actors"`
	ProhibitRequester  bool                     `json:"prohibit_requester"`
	RequiredPermission string                   `json:"required_permission,omitempty"`
	ObservedAt         time.Time                `json:"observed_at"`
	EligibleApprovals  []ApprovalEvidenceRecord `json:"eligible_approvals"`
}

type ApprovalEvidenceInput struct {
	Change         change.Snapshot
	RevisionDigest string
	ObservedAt     time.Time
}

// BuildApprovalEvidence binds approval review evidence to the exact Change
// revision and its authoritative digest. It reproduces the effective approval
// policy without exposing permission arrays and rejects inconsistent source
// state instead of presenting optimistic or partial evidence.
func BuildApprovalEvidence(input ApprovalEvidenceInput) (ApprovalEvidence, error) {
	snapshot := input.Change
	if err := validateApprovalIdentity("change id", snapshot.ID); err != nil {
		return ApprovalEvidence{}, err
	}
	if err := validateApprovalIdentity("revision id", snapshot.RevisionID); err != nil {
		return ApprovalEvidence{}, err
	}
	if err := validateApprovalIdentity("requester", snapshot.Requester); err != nil {
		return ApprovalEvidence{}, err
	}
	if !validApprovalDigest(input.RevisionDigest) {
		return ApprovalEvidence{}, fmt.Errorf("%w: canonical revision digest is required", ErrInvalidApprovalEvidence)
	}
	if snapshot.Version == 0 || snapshot.UpdatedAt.IsZero() {
		return ApprovalEvidence{}, fmt.Errorf("%w: complete Change state is required", ErrInvalidApprovalEvidence)
	}
	if input.ObservedAt.IsZero() {
		return ApprovalEvidence{}, fmt.Errorf("%w: observed time is required", ErrInvalidApprovalEvidence)
	}
	observedAt := input.ObservedAt.UTC()
	if snapshot.UpdatedAt.After(observedAt) {
		return ApprovalEvidence{}, fmt.Errorf("%w: Change state is newer than observed evidence", ErrInvalidApprovalEvidence)
	}
	if err := snapshot.Decision.Validate(); err != nil || snapshot.Risk != snapshot.Decision.Risk {
		return ApprovalEvidence{}, fmt.Errorf("%w: invalid policy decision", ErrInvalidApprovalEvidence)
	}
	if err := validateApprovalIdentity("policy id", snapshot.Decision.PolicyID); err != nil {
		return ApprovalEvidence{}, err
	}
	requirement := snapshot.Decision.Requirement
	if requirement.Permission != "" {
		if err := validateApprovalIdentity("required permission", requirement.Permission); err != nil {
			return ApprovalEvidence{}, err
		}
	}
	if requirement.Minimum > MaxApprovalEvidenceRecords {
		return ApprovalEvidence{}, fmt.Errorf("%w: approval requirement exceeds bounded review surface", ErrInvalidApprovalEvidence)
	}
	if len(snapshot.Approvals) > MaxApprovalEvidenceRecords {
		return ApprovalEvidence{}, fmt.Errorf("%w: approval records exceed bounded review surface", ErrInvalidApprovalEvidence)
	}
	if snapshot.Decision.Effect == policy.EffectDeny && requirement.Minimum != 0 {
		return ApprovalEvidence{}, fmt.Errorf("%w: denied policy cannot require approvals", ErrInvalidApprovalEvidence)
	}

	eligible, err := eligibleApprovalRecords(snapshot.Requester, requirement, snapshot.Approvals, observedAt)
	if err != nil {
		return ApprovalEvidence{}, err
	}
	policySatisfied := policy.CheckApprovals(snapshot.Requester, requirement, snapshot.Approvals) == nil
	if policySatisfied != (len(eligible) >= requirement.Minimum) {
		return ApprovalEvidence{}, fmt.Errorf("%w: approval projection differs from policy result", ErrInvalidApprovalEvidence)
	}

	state := ApprovalEvidencePending
	satisfied := policySatisfied
	switch snapshot.Decision.Effect {
	case policy.EffectDeny:
		state = ApprovalEvidenceDenied
		satisfied = false
		if snapshot.State != change.StateRejected {
			return ApprovalEvidence{}, fmt.Errorf("%w: denied policy has non-rejected Change state", ErrInvalidApprovalEvidence)
		}
	case policy.EffectAllow:
		if requirement.Minimum == 0 {
			state = ApprovalEvidenceNotRequired
			satisfied = true
		} else if policySatisfied {
			state = ApprovalEvidenceSatisfied
		}
	default:
		return ApprovalEvidence{}, fmt.Errorf("%w: unsupported policy effect", ErrInvalidApprovalEvidence)
	}

	if err := validateApprovalStateConsistency(snapshot.State, state); err != nil {
		return ApprovalEvidence{}, err
	}

	// The source order is relevant to policy evaluation when duplicate actors
	// exist, but the read-only output is canonicalized after eligibility is
	// determined so equal evidence serializes deterministically.
	sort.Slice(eligible, func(i, j int) bool {
		if !eligible[i].ApprovedAt.Equal(eligible[j].ApprovedAt) {
			return eligible[i].ApprovedAt.Before(eligible[j].ApprovedAt)
		}
		return eligible[i].Actor < eligible[j].Actor
	})

	return ApprovalEvidence{
		ContractVersion:    ApprovalEvidenceContractVersion,
		ChangeID:           snapshot.ID,
		RevisionID:         snapshot.RevisionID,
		RevisionDigest:     input.RevisionDigest,
		PolicyID:           snapshot.Decision.PolicyID,
		Risk:               snapshot.Risk,
		State:              state,
		RequiredCount:      requirement.Minimum,
		RecordedCount:      len(snapshot.Approvals),
		EligibleCount:      len(eligible),
		Satisfied:          satisfied,
		DistinctActors:     requirement.DistinctActors,
		ProhibitRequester:  requirement.ProhibitRequester,
		RequiredPermission: requirement.Permission,
		ObservedAt:         observedAt,
		EligibleApprovals:  eligible,
	}, nil
}

func eligibleApprovalRecords(requester string, requirement policy.ApprovalRequirement, approvals []policy.Approval, observedAt time.Time) ([]ApprovalEvidenceRecord, error) {
	seen := make(map[string]struct{}, len(approvals))
	eligible := make([]ApprovalEvidenceRecord, 0, len(approvals))
	for _, approval := range approvals {
		if err := validateApprovalIdentity("approval actor", approval.Actor); err != nil {
			return nil, err
		}
		if approval.ApprovedAt.IsZero() {
			return nil, fmt.Errorf("%w: approval time is required", ErrInvalidApprovalEvidence)
		}
		approvedAt := approval.ApprovedAt.UTC()
		if approvedAt.After(observedAt) {
			return nil, fmt.Errorf("%w: approval is future-dated", ErrInvalidApprovalEvidence)
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
		if requirement.Permission != "" && !approvalHasPermission(approval.Permissions, requirement.Permission) {
			continue
		}
		eligible = append(eligible, ApprovalEvidenceRecord{Actor: approval.Actor, ApprovedAt: approvedAt})
	}
	return eligible, nil
}

func approvalHasPermission(permissions []string, required string) bool {
	for _, permission := range permissions {
		if permission == required {
			return true
		}
	}
	return false
}

func validateApprovalIdentity(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(value) > MaxApprovalEvidenceIdentifierLength {
		return fmt.Errorf("%w: canonical %s is required", ErrInvalidApprovalEvidence, name)
	}
	return nil
}

func validApprovalDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, ch := range value[len("sha256:"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func validateApprovalStateConsistency(changeState change.State, approvalState ApprovalEvidenceState) error {
	switch changeState {
	case change.StatePendingApproval:
		if approvalState != ApprovalEvidencePending {
			return fmt.Errorf("%w: pending Change has non-pending approval evidence", ErrInvalidApprovalEvidence)
		}
	case change.StateApproved, change.StateQueued, change.StateExecuting, change.StateVerifying, change.StateSucceeded, change.StateFailed:
		if approvalState != ApprovalEvidenceSatisfied && approvalState != ApprovalEvidenceNotRequired {
			return fmt.Errorf("%w: progressed Change lacks satisfied approval evidence", ErrInvalidApprovalEvidence)
		}
	case change.StateRejected:
		// Rejection is valid for a deny decision or an operator/policy rejection
		// while an allow decision was still pending.
		if approvalState != ApprovalEvidenceDenied && approvalState != ApprovalEvidencePending {
			return fmt.Errorf("%w: rejected Change has inconsistent approval evidence", ErrInvalidApprovalEvidence)
		}
	case change.StateCancelled:
		// Cancellation can occur before or after approvals are satisfied.
	case "":
		return fmt.Errorf("%w: Change state is required", ErrInvalidApprovalEvidence)
	default:
		return fmt.Errorf("%w: unsupported Change state", ErrInvalidApprovalEvidence)
	}
	return nil
}
