package executionguard

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/config"
	"control-center/internal/orchestration/policy"
)

const ContractVersion = "orchestration.execution-preflight/v1"

var ErrInvalidInput = errors.New("invalid execution preflight input")

type MaintenanceWindow struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type Input struct {
	Change                   change.Snapshot
	Revision                 config.Revision
	CurrentRevisionID        string
	RequireMaintenanceWindow bool
	MaintenanceWindow        *MaintenanceWindow
	Now                      time.Time
}

// Decision is side-effect-free admission evidence. It intentionally never
// authorizes execution: callers still have to perform the normal RBAC, state
// transition, durable Job creation and Audit checks after evaluating it.
type Decision struct {
	ContractVersion     string             `json:"contract_version"`
	Eligible            bool               `json:"eligible"`
	ExecutionAuthorized bool               `json:"execution_authorized"`
	ChangeID            string             `json:"change_id"`
	ChangeVersion       uint64             `json:"change_version"`
	RevisionID          string             `json:"revision_id"`
	RevisionDigest      string             `json:"revision_digest"`
	EvaluatedAt         time.Time          `json:"evaluated_at"`
	MaintenanceWindow   *MaintenanceWindow `json:"maintenance_window,omitempty"`
	Blockers            []string           `json:"blockers"`
}

// Evaluate revalidates the exact Change/revision/policy/approval evidence at
// the last side-effect-free boundary before durable Job admission. It fails
// closed when evidence is malformed and reports explicit blockers when valid
// evidence is not currently eligible for admission.
func Evaluate(in Input) (Decision, error) {
	if in.Now.IsZero() {
		return Decision{}, invalid("evaluation time is required")
	}
	if strings.TrimSpace(in.CurrentRevisionID) == "" || in.CurrentRevisionID != strings.TrimSpace(in.CurrentRevisionID) {
		return Decision{}, invalid("current revision id must be canonical")
	}
	if err := validateChange(in.Change); err != nil {
		return Decision{}, err
	}
	if in.Revision.ID() == "" || in.Revision.Digest() == "" {
		return Decision{}, invalid("exact immutable revision is required")
	}
	if in.Change.UpdatedAt.After(in.Now) {
		return Decision{}, invalid("change evidence is newer than evaluation time")
	}
	if err := in.Change.Decision.Validate(); err != nil {
		return Decision{}, invalid("policy decision is invalid: %v", err)
	}
	if in.Change.Risk != in.Change.Decision.Risk {
		return Decision{}, invalid("change risk does not match policy risk")
	}

	now := in.Now.UTC()
	result := Decision{
		ContractVersion:     ContractVersion,
		ExecutionAuthorized: false,
		ChangeID:            in.Change.ID,
		ChangeVersion:       in.Change.Version,
		RevisionID:          in.Revision.ID(),
		RevisionDigest:      in.Revision.Digest(),
		EvaluatedAt:         now,
		Blockers:            []string{},
	}
	if in.Change.State != change.StateApproved {
		result.Blockers = append(result.Blockers, "change_not_approved")
	}
	if in.Change.Decision.Effect != policy.EffectAllow {
		result.Blockers = append(result.Blockers, "policy_denied")
	}
	if in.Change.RevisionID != in.Revision.ID() {
		result.Blockers = append(result.Blockers, "revision_binding_mismatch")
	}
	if in.CurrentRevisionID != in.Change.RevisionID {
		result.Blockers = append(result.Blockers, "revision_is_no_longer_current")
	}
	if err := policy.CheckApprovals(in.Change.Requester, in.Change.Decision.Requirement, in.Change.Approvals); err != nil {
		result.Blockers = append(result.Blockers, "approval_requirement_not_satisfied")
	}
	if in.MaintenanceWindow != nil {
		window, err := normalizeWindow(*in.MaintenanceWindow)
		if err != nil {
			return Decision{}, err
		}
		result.MaintenanceWindow = &window
		if now.Before(window.StartsAt) {
			result.Blockers = append(result.Blockers, "maintenance_window_not_started")
		}
		if !now.Before(window.EndsAt) {
			result.Blockers = append(result.Blockers, "maintenance_window_closed")
		}
	} else if in.RequireMaintenanceWindow {
		result.Blockers = append(result.Blockers, "maintenance_window_required")
	}
	result.Eligible = len(result.Blockers) == 0
	return result, nil
}

func validateChange(snapshot change.Snapshot) error {
	if strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.Action) == "" ||
		strings.TrimSpace(snapshot.Requester) == "" || strings.TrimSpace(snapshot.RevisionID) == "" {
		return invalid("change identity, action, requester and revision are required")
	}
	if snapshot.ID != strings.TrimSpace(snapshot.ID) || snapshot.Action != strings.TrimSpace(snapshot.Action) ||
		snapshot.Requester != strings.TrimSpace(snapshot.Requester) || snapshot.RevisionID != strings.TrimSpace(snapshot.RevisionID) {
		return invalid("change identifiers must be canonical")
	}
	if snapshot.Version == 0 || snapshot.UpdatedAt.IsZero() {
		return invalid("change version and timestamp are required")
	}
	if !snapshot.Risk.Valid() {
		return invalid("change risk is invalid")
	}
	return nil
}

func normalizeWindow(window MaintenanceWindow) (MaintenanceWindow, error) {
	if window.StartsAt.IsZero() || window.EndsAt.IsZero() || !window.EndsAt.After(window.StartsAt) {
		return MaintenanceWindow{}, invalid("maintenance window is invalid")
	}
	duration := window.EndsAt.Sub(window.StartsAt)
	if duration < time.Minute || duration > 7*24*time.Hour {
		return MaintenanceWindow{}, invalid("maintenance window must be between one minute and seven days")
	}
	return MaintenanceWindow{StartsAt: window.StartsAt.UTC(), EndsAt: window.EndsAt.UTC()}, nil
}

func invalid(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, values...))
}
