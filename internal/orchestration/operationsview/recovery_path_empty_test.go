package operationsview

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/recovery"
)

func TestBuildRecoveryPathEvidenceRepresentsEmptyCreatingPointAsBlocked(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	evidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		recoveryPathTestDigest,
		RecoveryPathObservation{
			RecoveryPointID:     "rp-creating",
			RecoveryPointState:  recovery.RecoveryPointCreating,
			BackupCount:         0,
			VerifiedBackupCount: 0,
			VerificationOutcome: recovery.VerificationPending,
		},
		now,
	)
	if err != nil {
		t.Fatalf("BuildRecoveryPathEvidence() error = %v", err)
	}
	if evidence.State != RecoveryPathBlocked || evidence.BlockReason != RecoveryBlockPointNotReady {
		t.Fatalf("state=%q reason=%q, want blocked/not-ready", evidence.State, evidence.BlockReason)
	}
	if err := ValidateRecoveryPathEvidence(evidence); err != nil {
		t.Fatalf("ValidateRecoveryPathEvidence() error = %v", err)
	}
}

func TestBuildRecoveryPathEvidenceRejectsPassedVerificationWithoutBackups(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	verifiedAt := now.Add(-time.Minute)
	_, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		recoveryPathTestDigest,
		RecoveryPathObservation{
			RecoveryPointID:        "rp-empty",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            0,
			VerifiedBackupCount:    0,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now,
	)
	if !errors.Is(err, ErrInvalidRecoveryPathEvidence) {
		t.Fatalf("error = %v, want ErrInvalidRecoveryPathEvidence", err)
	}
}
