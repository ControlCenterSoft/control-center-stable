package operationsview

import (
	"time"

	"control-center/internal/recovery"
)

const MaximumRecoveryVerificationAge = 365 * 24 * time.Hour

type RecoveryPathGraphInput struct {
	ChangeID           string
	RevisionID         string
	RevisionDigest     string
	RecoveryPointID    string
	Graph              recovery.RecoveryMetadataGraph
	VerificationMaxAge time.Duration
	EvaluatedAt        time.Time
}

// BuildRecoveryPathEvidenceFromGraph derives the operator projection from one
// complete, integrity-validated recovery metadata graph. This is the preferred
// runtime adapter: it prevents callers from promoting arbitrary client-supplied
// counters into a ready recovery claim.
func BuildRecoveryPathEvidenceFromGraph(input RecoveryPathGraphInput) (RecoveryPathEvidence, error) {
	changeID, err := normalizeRecoveryPathIdentifier("change_id", input.ChangeID)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	recoveryPointID, err := normalizeRecoveryPathIdentifier("recovery_point_id", input.RecoveryPointID)
	if err != nil {
		return RecoveryPathEvidence{}, err
	}
	if input.EvaluatedAt.IsZero() {
		return RecoveryPathEvidence{}, invalidRecoveryPath("evaluated_at is required")
	}
	now := input.EvaluatedAt.UTC()
	if input.VerificationMaxAge <= 0 || input.VerificationMaxAge > MaximumRecoveryVerificationAge || input.VerificationMaxAge%time.Second != 0 {
		return RecoveryPathEvidence{}, invalidRecoveryPath("verification_max_age must be whole seconds within (0,365d]")
	}
	if err := recovery.ValidateRecoveryMetadataGraph(input.Graph); err != nil {
		return RecoveryPathEvidence{}, invalidRecoveryPath("recovery metadata graph is invalid: %v", err)
	}

	var point *recovery.RecoveryPoint
	for index := range input.Graph.RecoveryPoints {
		candidate := &input.Graph.RecoveryPoints[index]
		if candidate.ObjectID == recoveryPointID {
			point = candidate
			break
		}
	}
	if point == nil {
		return RecoveryPathEvidence{}, invalidRecoveryPath("recovery_point_id does not exist in the authoritative graph")
	}
	if point.ChangeID != changeID {
		return RecoveryPathEvidence{}, invalidRecoveryPath("recovery point is bound to a different change")
	}
	if point.Trigger != recovery.TriggerPreChange && point.Trigger != recovery.TriggerPreDestructive {
		return RecoveryPathEvidence{}, invalidRecoveryPath("recovery point is not a pre-change recovery boundary")
	}
	if point.CreatedAt.After(now) || point.UpdatedAt.After(now) {
		return RecoveryPathEvidence{}, invalidRecoveryPath("recovery point metadata is future-dated")
	}

	backups := make(map[string]recovery.BackupMetadata, len(input.Graph.Backups))
	for _, backup := range input.Graph.Backups {
		backups[backup.ObjectID] = backup
	}

	verifiedBackups := 0
	var oldestVerifiedAt *time.Time
	var effectiveExpiry *time.Time
	if point.ExpiresAt != nil {
		expiresAt := point.ExpiresAt.UTC()
		effectiveExpiry = &expiresAt
	}
	for _, backupID := range point.BackupIDs {
		backup, exists := backups[backupID]
		if !exists {
			return RecoveryPathEvidence{}, invalidRecoveryPath("recovery point references unavailable backup %q", backupID)
		}
		if backup.UpdatedAt.After(now) {
			return RecoveryPathEvidence{}, invalidRecoveryPath("backup %q metadata is future-dated", backupID)
		}
		if backup.Health.State != recovery.BackupHealthVerified {
			continue
		}
		if backup.Health.LastVerifiedAt == nil {
			return RecoveryPathEvidence{}, invalidRecoveryPath("verified backup %q is missing last_verified_at", backupID)
		}
		verifiedAt := backup.Health.LastVerifiedAt.UTC()
		if verifiedAt.After(now) {
			return RecoveryPathEvidence{}, invalidRecoveryPath("backup %q verification is future-dated", backupID)
		}
		verifiedBackups++
		if oldestVerifiedAt == nil || verifiedAt.Before(*oldestVerifiedAt) {
			copy := verifiedAt
			oldestVerifiedAt = &copy
		}
		deadline := verifiedAt.Add(input.VerificationMaxAge)
		effectiveExpiry = earlierRecoveryPathTime(effectiveExpiry, &deadline)
	}

	verificationOutcome := recovery.VerificationPending
	var verificationObservedAt *time.Time
	if len(point.BackupIDs) > 0 && verifiedBackups == len(point.BackupIDs) && oldestVerifiedAt != nil {
		verificationOutcome = recovery.VerificationPassed
		verificationObservedAt = oldestVerifiedAt
	}

	return BuildRecoveryPathEvidence(
		changeID,
		input.RevisionID,
		input.RevisionDigest,
		RecoveryPathObservation{
			RecoveryPointID:        point.ObjectID,
			RecoveryPointState:     point.State,
			BackupCount:            len(point.BackupIDs),
			VerifiedBackupCount:    verifiedBackups,
			VerificationOutcome:    verificationOutcome,
			VerificationObservedAt: verificationObservedAt,
			ExpiresAt:              effectiveExpiry,
		},
		now,
	)
}

func earlierRecoveryPathTime(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return current
	}
	candidateUTC := candidate.UTC()
	if current == nil || candidateUTC.Before(*current) {
		return &candidateUTC
	}
	return current
}
