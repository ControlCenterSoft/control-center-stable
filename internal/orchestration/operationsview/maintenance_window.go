package operationsview

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaintenanceWindowContractVersion = "ui.operations-maintenance-window/v1"

const (
	MinimumMaintenanceWindowDuration = time.Minute
	MaximumMaintenanceWindowDuration = 7 * 24 * time.Hour
)

var ErrInvalidMaintenanceWindowEvidence = errors.New("invalid maintenance window evidence")

type MaintenanceWindowState string

const (
	MaintenanceWindowNotRequired MaintenanceWindowState = "not_required"
	MaintenanceWindowScheduled   MaintenanceWindowState = "scheduled"
	MaintenanceWindowOpen        MaintenanceWindowState = "open"
	MaintenanceWindowExpired     MaintenanceWindowState = "expired"
)

type MaintenanceWindow struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// MaintenanceWindowEvidence is read-only operator evidence for one exact
// immutable Change revision. It never grants approval or execution authority.
type MaintenanceWindowEvidence struct {
	ContractVersion           string                 `json:"contract_version"`
	ChangeID                  string                 `json:"change_id"`
	RevisionID                string                 `json:"revision_id"`
	RevisionDigest            string                 `json:"revision_digest"`
	Required                  bool                   `json:"required"`
	Window                    *MaintenanceWindow     `json:"window,omitempty"`
	EvaluatedAt               time.Time              `json:"evaluated_at"`
	State                     MaintenanceWindowState `json:"state"`
	ExecutionAuthorized       bool                   `json:"execution_authorized"`
	ProductionMutationAllowed bool                   `json:"production_mutation_allowed"`
}

func BuildMaintenanceWindowEvidence(
	changeID string,
	revisionID string,
	revisionDigest string,
	required bool,
	window *MaintenanceWindow,
	now time.Time,
) (MaintenanceWindowEvidence, error) {
	changeID, err := normalizeMaintenanceWindowIdentifier("change_id", changeID)
	if err != nil {
		return MaintenanceWindowEvidence{}, err
	}
	revisionID, err = normalizeMaintenanceWindowIdentifier("revision_id", revisionID)
	if err != nil {
		return MaintenanceWindowEvidence{}, err
	}
	if !validMaintenanceWindowDigest(revisionDigest) {
		return MaintenanceWindowEvidence{}, invalidMaintenanceWindow("revision_digest must be a lowercase sha256 digest")
	}
	if now.IsZero() {
		return MaintenanceWindowEvidence{}, invalidMaintenanceWindow("evaluated_at is required")
	}
	now = now.UTC()

	evidence := MaintenanceWindowEvidence{
		ContractVersion:           MaintenanceWindowContractVersion,
		ChangeID:                  changeID,
		RevisionID:                revisionID,
		RevisionDigest:            revisionDigest,
		Required:                  required,
		EvaluatedAt:               now,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}

	if !required {
		if window != nil {
			return MaintenanceWindowEvidence{}, invalidMaintenanceWindow("window must be omitted when maintenance window is not required")
		}
		evidence.State = MaintenanceWindowNotRequired
		return evidence, nil
	}
	if window == nil {
		return MaintenanceWindowEvidence{}, invalidMaintenanceWindow("window is required")
	}
	normalizedWindow, err := normalizeMaintenanceWindow(*window)
	if err != nil {
		return MaintenanceWindowEvidence{}, err
	}
	evidence.Window = &normalizedWindow
	switch {
	case now.Before(normalizedWindow.StartsAt):
		evidence.State = MaintenanceWindowScheduled
	case now.Before(normalizedWindow.EndsAt):
		evidence.State = MaintenanceWindowOpen
	default:
		evidence.State = MaintenanceWindowExpired
	}
	return evidence, nil
}

// ValidateMaintenanceWindowEvidence validates evidence received across a
// transport/storage boundary by rebuilding the deterministic contract from
// its inputs. Authority flags are hard-false: a maintenance window never
// grants execution or production-mutation permission on its own.
func ValidateMaintenanceWindowEvidence(evidence MaintenanceWindowEvidence) error {
	if evidence.ContractVersion != MaintenanceWindowContractVersion {
		return invalidMaintenanceWindow("contract_version is invalid")
	}
	if evidence.ExecutionAuthorized || evidence.ProductionMutationAllowed {
		return invalidMaintenanceWindow("maintenance window evidence must not grant execution authority")
	}

	rebuilt, err := BuildMaintenanceWindowEvidence(
		evidence.ChangeID,
		evidence.RevisionID,
		evidence.RevisionDigest,
		evidence.Required,
		evidence.Window,
		evidence.EvaluatedAt,
	)
	if err != nil {
		return err
	}
	if evidence.State != rebuilt.State {
		return invalidMaintenanceWindow("state is inconsistent with evaluated_at and window")
	}
	if !evidence.EvaluatedAt.Equal(rebuilt.EvaluatedAt) {
		return invalidMaintenanceWindow("evaluated_at is inconsistent")
	}
	if (evidence.Window == nil) != (rebuilt.Window == nil) {
		return invalidMaintenanceWindow("window presence is inconsistent")
	}
	if evidence.Window != nil {
		if !evidence.Window.StartsAt.Equal(rebuilt.Window.StartsAt) || !evidence.Window.EndsAt.Equal(rebuilt.Window.EndsAt) {
			return invalidMaintenanceWindow("window timestamps are inconsistent")
		}
	}
	return nil
}

func normalizeMaintenanceWindow(window MaintenanceWindow) (MaintenanceWindow, error) {
	if window.StartsAt.IsZero() || window.EndsAt.IsZero() {
		return MaintenanceWindow{}, invalidMaintenanceWindow("starts_at and ends_at are required")
	}
	startsAt := window.StartsAt.UTC()
	endsAt := window.EndsAt.UTC()
	if !endsAt.After(startsAt) {
		return MaintenanceWindow{}, invalidMaintenanceWindow("ends_at must be after starts_at")
	}
	duration := endsAt.Sub(startsAt)
	if duration < MinimumMaintenanceWindowDuration || duration > MaximumMaintenanceWindowDuration {
		return MaintenanceWindow{}, invalidMaintenanceWindow("window duration must be between one minute and seven days")
	}
	return MaintenanceWindow{StartsAt: startsAt, EndsAt: endsAt}, nil
}

func normalizeMaintenanceWindowIdentifier(field, value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" || normalized != value || len(normalized) > 255 {
		return "", invalidMaintenanceWindow("%s is invalid", field)
	}
	return normalized, nil
}

func validMaintenanceWindowDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func invalidMaintenanceWindow(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidMaintenanceWindowEvidence, fmt.Sprintf(format, args...))
}
