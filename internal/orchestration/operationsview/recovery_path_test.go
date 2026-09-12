package operationsview

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/recovery"
)

const recoveryPathTestDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestBuildRecoveryPathEvidenceReady(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 20, 0, 0, time.UTC)
	verifiedAt := now.Add(-10 * time.Minute)
	expiresAt := now.Add(6 * time.Hour)

	evidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		recoveryPathTestDigest,
		RecoveryPathObservation{
			RecoveryPointID:        "recovery-point-31",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            2,
			VerifiedBackupCount:    2,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
			ExpiresAt:              &expiresAt,
		},
		now,
	)
	if err != nil {
		t.Fatalf("BuildRecoveryPathEvidence() error = %v", err)
	}
	if evidence.State != RecoveryPathReady || evidence.BlockReason != RecoveryBlockNone {
		t.Fatalf("unexpected readiness: state=%q reason=%q", evidence.State, evidence.BlockReason)
	}
	if evidence.ExecutionAuthorized || evidence.ProductionMutationAllowed {
		t.Fatal("read-only recovery evidence granted mutation authority")
	}
	if err := ValidateRecoveryPathEvidence(evidence); err != nil {
		t.Fatalf("ValidateRecoveryPathEvidence() error = %v", err)
	}
}

func TestBuildRecoveryPathEvidenceFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 20, 0, 0, time.UTC)
	verifiedAt := now.Add(-10 * time.Minute)
	futureExpiry := now.Add(time.Hour)
	pastExpiry := now.Add(-time.Second)

	tests := []struct {
		name        string
		observation RecoveryPathObservation
		wantState   RecoveryPathState
		wantReason  RecoveryPathBlockReason
	}{
		{
			name: "recovery point not ready",
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp-partial", RecoveryPointState: recovery.RecoveryPointPartial,
				BackupCount: 2, VerifiedBackupCount: 2, VerificationOutcome: recovery.VerificationPassed,
				VerificationObservedAt: &verifiedAt, ExpiresAt: &futureExpiry,
			},
			wantState: RecoveryPathBlocked, wantReason: RecoveryBlockPointNotReady,
		},
		{
			name: "backup unverified",
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp-backup", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 2, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
				VerificationObservedAt: &verifiedAt, ExpiresAt: &futureExpiry,
			},
			wantState: RecoveryPathBlocked, wantReason: RecoveryBlockBackupUnverified,
		},
		{
			name: "restore unverified",
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp-restore", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 2, VerifiedBackupCount: 2, VerificationOutcome: recovery.VerificationPending,
				ExpiresAt: &futureExpiry,
			},
			wantState: RecoveryPathBlocked, wantReason: RecoveryBlockRestoreUnverified,
		},
		{
			name: "expired evidence",
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp-expired", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
				VerificationObservedAt: &verifiedAt, ExpiresAt: &pastExpiry,
			},
			wantState: RecoveryPathExpired, wantReason: RecoveryBlockEvidenceExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence, err := BuildRecoveryPathEvidence("change-31", "revision-31", recoveryPathTestDigest, tt.observation, now)
			if err != nil {
				t.Fatalf("BuildRecoveryPathEvidence() error = %v", err)
			}
			if evidence.State != tt.wantState || evidence.BlockReason != tt.wantReason {
				t.Fatalf("state=%q reason=%q, want state=%q reason=%q", evidence.State, evidence.BlockReason, tt.wantState, tt.wantReason)
			}
		})
	}
}

func TestBuildRecoveryPathEvidenceRejectsMalformedObservation(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 20, 0, 0, time.UTC)
	futureVerification := now.Add(time.Second)

	tests := []struct {
		name        string
		digest      string
		observation RecoveryPathObservation
	}{
		{
			name:   "invalid digest",
			digest: "sha256:not-a-digest",
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPending,
			},
		},
		{
			name:   "verified count exceeds backups",
			digest: recoveryPathTestDigest,
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 1, VerifiedBackupCount: 2, VerificationOutcome: recovery.VerificationPending,
			},
		},
		{
			name:   "passed without timestamp",
			digest: recoveryPathTestDigest,
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
			},
		},
		{
			name:   "future verification",
			digest: recoveryPathTestDigest,
			observation: RecoveryPathObservation{
				RecoveryPointID: "rp", RecoveryPointState: recovery.RecoveryPointReady,
				BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
				VerificationObservedAt: &futureVerification,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildRecoveryPathEvidence("change-31", "revision-31", tt.digest, tt.observation, now)
			if !errors.Is(err, ErrInvalidRecoveryPathEvidence) {
				t.Fatalf("error = %v, want ErrInvalidRecoveryPathEvidence", err)
			}
		})
	}
}

func TestValidateRecoveryPathEvidenceRejectsTampering(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 20, 0, 0, time.UTC)
	verifiedAt := now.Add(-time.Minute)
	evidence, err := BuildRecoveryPathEvidence(
		"change-31", "revision-31", recoveryPathTestDigest,
		RecoveryPathObservation{
			RecoveryPointID: "rp", RecoveryPointState: recovery.RecoveryPointReady,
			BackupCount: 1, VerifiedBackupCount: 1, VerificationOutcome: recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*RecoveryPathEvidence)
	}{
		{name: "contract", mutate: func(item *RecoveryPathEvidence) { item.ContractVersion = "unknown/v1" }},
		{name: "authority", mutate: func(item *RecoveryPathEvidence) { item.ExecutionAuthorized = true }},
		{name: "state", mutate: func(item *RecoveryPathEvidence) { item.State = RecoveryPathBlocked }},
		{name: "reason", mutate: func(item *RecoveryPathEvidence) { item.BlockReason = RecoveryBlockBackupUnverified }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copy := evidence
			tt.mutate(&copy)
			if !errors.Is(ValidateRecoveryPathEvidence(copy), ErrInvalidRecoveryPathEvidence) {
				t.Fatalf("tampered evidence accepted: %#v", copy)
			}
		})
	}
}
