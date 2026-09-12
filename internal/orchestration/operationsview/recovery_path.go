package operationsview

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"control-center/internal/recovery"
)

const RecoveryPathEvidenceContractVersion = "ui.operations-recovery-path/v1"

var ErrInvalidRecoveryPathEvidence = errors.New("invalid operations recovery path evidence")

type RecoveryPathState string

const (
	RecoveryPathReady   RecoveryPathState = "ready"
	RecoveryPathBlocked RecoveryPathState = "blocked"
	RecoveryPathExpired RecoveryPathState = "expired"
)

type RecoveryPathBlockReason string

const (
	RecoveryBlockNone              RecoveryPathBlockReason = ""
	RecoveryBlockPointNotReady     RecoveryPathBlockReason = "recovery_point_not_ready"
	RecoveryBlockBackupUnverified  RecoveryPathBlockReason = "backup_not_verified"
	RecoveryBlockRestoreUnverified RecoveryPathBlockReason = "restore_not_verified"
	RecoveryBlockEvidenceExpired   RecoveryPathBlockReason = "evidence_expired"
)

// RecoveryPathObservation is a bounded read-only observation from the recovery
// subsystem. It intentionally carries no artifact location, provider endpoint,
// executable operation, credentials or mutable policy material.
type RecoveryPathObservation struct {
	RecoveryPointID        string
	RecoveryPointState     recovery.RecoveryPointState
	BackupCount            int
	VerifiedBackupCount    int
	VerificationOutcome    recovery.VerificationOutcome
	VerificationObservedAt *time.Time
	ExpiresAt              *time.Time
}

// RecoveryPathEvidence is operator-safe evidence that an exact immutable
// Change revision has (or does not yet have) a qualified recovery path. The
// contract is descriptive only and cannot authorize execution or production
// mutation.
type RecoveryPathEvidence struct {
	ContractVersion           string                       `json:"contract_version"`
	ChangeID                  string                       `json:"change_id"`
	RevisionID                string                       `json:"revision_id"`
	RevisionDigest            string                       `json:"revision_digest"`
	RecoveryPointID           string                       `json:"recovery_point_id"`
	RecoveryPointState        recovery.RecoveryPointState  `json:"recovery_point_state"`
	BackupCount               int                          `json:"backup_count"`
	VerifiedBackupCount       int                          `json:"verified_backup_count"`
	VerificationOutcome       recovery.VerificationOutcome `json:"verification_outcome"`
	VerificationObservedAt    *time.Time                   `json:"verification_observed_at,omitempty"`
	ExpiresAt                 *time.Time                   `json:"expires_at,omitempty"`
	EvaluatedAt               time.Time                    `json:"evaluated_at"`
	State                     RecoveryPathState            `json:"state"`
	BlockReason               RecoveryPathBlockReason      `json:"block_reason,omitempty"`
	ExecutionAuthorized       bool                         `json:"execution_authorized"`
	ProductionMutationAllowed bool                         `json:"production_mutation_allowed"`
}

func BuildRecoveryPathEvidence(
	changeID string,
	revisionID string,
	revisionDigest string,
	observation RecoveryPathObservation,
	now time.Time,
) (RecoveryPathEvidence, error) {
	changeID, err := normalizeRecoveryPathIdentifier("change_id", changeID)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	revisionID, err = normalizeRecoveryPathIdentifier("revision_id", revisionID)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	if !validRecoveryPathDigest(revisionDigest) {
		return RecoveryPathEvidence{}, invalidRecoveryPath("revision_digest must be a lowercase sha256 digest")
	}
	observation.RecoveryPointID, err = normalizeRecoveryPathIdentifier("recovery_point_id", observation.RecoveryPointID)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	if now.IsZero() {
		return RecoveryPathEvidence{}, invalidRecoveryPath("evaluated_at is required")
	}
	now = now.UTC()
	if observation.BackupCount < 0 {
		return RecoveryPathEvidence{}, invalidRecoveryPath("backup_count must be non-negative")
	}
	if observation.VerifiedBackupCount < 0 || observation.VerifiedBackupCount > observation.BackupCount {
		return RecoveryPathEvidence{}, invalidRecoveryPath("verified_backup_count is outside backup_count")
	}
	if !validRecoveryPointState(observation.RecoveryPointState) {
		return RecoveryPathEvidence{}, invalidRecoveryPath("recovery_point_state is invalid")
	}
	if !validRecoveryVerificationOutcome(observation.VerificationOutcome) {
		return RecoveryPathEvidence{}, invalidRecoveryPath("verification_outcome is invalid")
	}
	if observation.BackupCount == 0 && observation.VerificationOutcome == recovery.VerificationPassed {
		return RecoveryPathEvidence{}, invalidRecoveryPath("passed verification requires at least one backup")
	}

	verifiedAt, err := normalizeOptionalRecoveryPathTime("verification_observed_at", observation.VerificationObservedAt, now)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	expiresAt, err := normalizeOptionalRecoveryPathTime("expires_at", observation.ExpiresAt, now)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	if observation.VerificationOutcome == recovery.VerificationPassed && verifiedAt == nil {
		return RecoveryPathEvidence{}, invalidRecoveryPath("passed verification requires verification_observed_at")
	}
	if observation.VerificationOutcome != recovery.VerificationPassed && verifiedAt != nil {
		return RecoveryPathEvidence{}, invalidRecoveryPath("verification_observed_at is allowed only for passed verification")
	}

	evidence := RecoveryPathEvidence{
		ContractVersion:           RecoveryPathEvidenceContractVersion,
		ChangeID:                  changeID,
		RevisionID:                revisionID,
		RevisionDigest:            revisionDigest,
		RecoveryPointID:           observation.RecoveryPointID,
		RecoveryPointState:        observation.RecoveryPointState,
		BackupCount:               observation.BackupCount,
		VerifiedBackupCount:       observation.VerifiedBackupCount,
		VerificationOutcome:       observation.VerificationOutcome,
		VerificationObservedAt:    verifiedAt,
		ExpiresAt:                 expiresAt,
		EvaluatedAt:               now,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}

	switch {
	case expiresAt != nil && !expiresAt.After(now):
		evidence.State = RecoveryPathExpired
		evidence.BlockReason = RecoveryBlockEvidenceExpired
	case observation.RecoveryPointState == recovery.RecoveryPointExpired:
		evidence.State = RecoveryPathExpired
		evidence.BlockReason = RecoveryBlockEvidenceExpired
	case observation.RecoveryPointState != recovery.RecoveryPointReady:
		evidence.State = RecoveryPathBlocked
		evidence.BlockReason = RecoveryBlockPointNotReady
	case observation.BackupCount == 0:
		evidence.State = RecoveryPathBlocked
		evidence.BlockReason = RecoveryBlockBackupUnverified
	case observation.VerifiedBackupCount != observation.BackupCount:
		evidence.State = RecoveryPathBlocked
		evidence.BlockReason = RecoveryBlockBackupUnverified
	case observation.VerificationOutcome != recovery.VerificationPassed:
		evidence.State = RecoveryPathBlocked
		evidence.BlockReason = RecoveryBlockRestoreUnverified
	default:
		evidence.State = RecoveryPathReady
		evidence.BlockReason = RecoveryBlockNone
	}
	return evidence, nil
}

// ValidateRecoveryPathEvidence validates an evidence record crossing a storage
// or transport boundary by rebuilding its deterministic state. It deliberately
// rejects any attempt to turn recovery evidence into execution authority.
func ValidateRecoveryPathEvidence(evidence RecoveryPathEvidence) error {
	if evidence.ContractVersion != RecoveryPathEvidenceContractVersion {
		return invalidRecoveryPath("contract_version is invalid")
	}
	if evidence.ExecutionAuthorized || evidence.ProductionMutationAllowed {
		return invalidRecoveryPath("recovery path evidence must not grant execution authority")
	}
	observation := RecoveryPathObservation{
		RecoveryPointID:        evidence.RecoveryPointID,
		RecoveryPointState:     evidence.RecoveryPointState,
		BackupCount:            evidence.BackupCount,
		VerifiedBackupCount:    evidence.VerifiedBackupCount,
		VerificationOutcome:    evidence.VerificationOutcome,
		VerificationObservedAt: evidence.VerificationObservedAt,
		ExpiresAt:              evidence.ExpiresAt,
	}
	rebuilt, err := BuildRecoveryPathEvidence(
		evidence.ChangeID,
		evidence.RevisionID,
		evidence.RevisionDigest,
		observation,
		evidence.EvaluatedAt,
	)
	if err != nil {
		return err
	}
	if evidence.State != rebuilt.State || evidence.BlockReason != rebuilt.BlockReason {
		return invalidRecoveryPath("state or block_reason is inconsistent with recovery observation")
	}
	if !evidence.EvaluatedAt.Equal(rebuilt.EvaluatedAt) {
		return invalidRecoveryPath("evaluated_at is inconsistent")
	}
	if !equalOptionalRecoveryPathTime(evidence.VerificationObservedAt, rebuilt.VerificationObservedAt) ||
		!equalOptionalRecoveryPathTime(evidence.ExpiresAt, rebuilt.ExpiresAt) {
		return invalidRecoveryPath("recovery timestamps are inconsistent")
	}
	return nil
}

func validRecoveryPointState(value recovery.RecoveryPointState) bool {
	switch value {
	case recovery.RecoveryPointCreating,
		recovery.RecoveryPointReady,
		recovery.RecoveryPointPartial,
		recovery.RecoveryPointFailed,
		recovery.RecoveryPointExpired:
		return true
	default:
		return false
	}
}

func validRecoveryVerificationOutcome(value recovery.VerificationOutcome) bool {
	switch value {
	case recovery.VerificationPending, recovery.VerificationPassed, recovery.VerificationFailed:
		return true
	default:
		return false
	}
}

func normalizeOptionalRecoveryPathTime(field string, value *time.Time, now time.Time) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if value.IsZero() {
		return nil, invalidRecoveryPath("%s must not be zero", field)
	}
	normalized := value.UTC()
	if field == "verification_observed_at" && normalized.After(now) {
		return nil, invalidRecoveryPath("verification_observed_at must not be in the future")
	}
	return &normalized, nil
}

func equalOptionalRecoveryPathTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func normalizeRecoveryPathIdentifier(field, value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" || normalized != value || len(normalized) > 255 {
		return "", invalidRecoveryPath("%s is invalid", field)
	}
	return normalized, nil
}

func validRecoveryPathDigest(value string) bool {
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

func invalidRecoveryPath(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRecoveryPathEvidence, fmt.Sprintf(format, args...))
}
