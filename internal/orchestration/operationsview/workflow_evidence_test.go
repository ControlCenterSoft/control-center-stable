package operationsview

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/recovery"
)

func TestBuildOperationsWorkflowEvidenceCompletesExactHealthyChain(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 45, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	outputDigest := "sha256:" + strings.Repeat("b", 64)
	verifiedAt := now.Add(-5 * time.Minute)

	recoveryEvidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		digest,
		RecoveryPathObservation{
			RecoveryPointID:        "rp-31",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            1,
			VerifiedBackupCount:    1,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	evidence, err := BuildOperationsWorkflowEvidence(OperationsWorkflowEvidenceInput{
		Approval: ApprovalEvidence{
			ContractVersion: ApprovalEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			State:           ApprovalEvidenceSatisfied,
			Satisfied:       true,
			ObservedAt:      now.Add(-8 * time.Minute),
		},
		Result: JobResultEvidence{
			ContractVersion: JobResultEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			JobID:           "job-31",
			JobVersion:      7,
			Outcome:         job.StatusSucceeded,
			OutputPresent:   true,
			OutputDigest:    outputDigest,
			HealthChecks:    1,
			AuditEvents:     1,
			WorstHealth:     events.HealthHealthy,
			ObservedAt:      now.Add(-2 * time.Minute),
		},
		Recovery:   recoveryEvidence,
		ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != OperationsWorkflowEvidenceComplete || !evidence.EvidenceComplete {
		t.Fatalf("exact healthy evidence chain was not complete: %#v", evidence)
	}
	if len(evidence.BlockReasons) != 0 {
		t.Fatalf("complete evidence contains blockers: %#v", evidence.BlockReasons)
	}
	if evidence.ExecutionAuthorized || evidence.ProductionMutationAllowed {
		t.Fatalf("read-only workflow evidence gained authority: %#v", evidence)
	}
	if evidence.ResultOutputDigest != outputDigest {
		t.Fatalf("result digest not preserved: %#v", evidence)
	}
}

func TestBuildOperationsWorkflowEvidenceRejectsCrossRevisionMismatch(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 45, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	otherDigest := "sha256:" + strings.Repeat("c", 64)
	verifiedAt := now.Add(-5 * time.Minute)
	recoveryEvidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		digest,
		RecoveryPathObservation{
			RecoveryPointID:        "rp-31",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            1,
			VerifiedBackupCount:    1,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = BuildOperationsWorkflowEvidence(OperationsWorkflowEvidenceInput{
		Approval: ApprovalEvidence{
			ContractVersion: ApprovalEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			State:           ApprovalEvidenceSatisfied,
			Satisfied:       true,
			ObservedAt:      now.Add(-8 * time.Minute),
		},
		Result: JobResultEvidence{
			ContractVersion: JobResultEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-other",
			RevisionDigest:  otherDigest,
			JobID:           "job-31",
			JobVersion:      7,
			Outcome:         job.StatusSucceeded,
			OutputPresent:   true,
			OutputDigest:    "sha256:" + strings.Repeat("b", 64),
			HealthChecks:    1,
			AuditEvents:     1,
			WorstHealth:     events.HealthHealthy,
			ObservedAt:      now.Add(-2 * time.Minute),
		},
		Recovery:   recoveryEvidence,
		ObservedAt: now,
	})
	if !errors.Is(err, ErrInvalidOperationsWorkflowEvidence) {
		t.Fatalf("cross-revision evidence must fail closed, got %v", err)
	}
}

func TestBuildOperationsWorkflowEvidenceBlocksIncompleteSafetyChain(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 45, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)

	recoveryEvidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		digest,
		RecoveryPathObservation{
			RecoveryPointID:     "rp-31",
			RecoveryPointState:  recovery.RecoveryPointPartial,
			BackupCount:         1,
			VerifiedBackupCount: 0,
			VerificationOutcome: recovery.VerificationPending,
		},
		now.Add(-4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	evidence, err := BuildOperationsWorkflowEvidence(OperationsWorkflowEvidenceInput{
		Approval: ApprovalEvidence{
			ContractVersion: ApprovalEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			State:           ApprovalEvidencePending,
			Satisfied:       false,
			ObservedAt:      now.Add(-8 * time.Minute),
		},
		Result: JobResultEvidence{
			ContractVersion: JobResultEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			JobID:           "job-31",
			JobVersion:      7,
			Outcome:         job.StatusSucceeded,
			OutputPresent:   true,
			OutputDigest:    "sha256:" + strings.Repeat("b", 64),
			HealthChecks:    1,
			AuditEvents:     0,
			WorstHealth:     events.HealthDegraded,
			ObservedAt:      now.Add(-2 * time.Minute),
		},
		Recovery:   recoveryEvidence,
		ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != OperationsWorkflowEvidenceBlocked || evidence.EvidenceComplete {
		t.Fatalf("incomplete safety evidence was not blocked: %#v", evidence)
	}
	want := []OperationsWorkflowBlockReason{
		OperationsWorkflowBlockApproval,
		OperationsWorkflowBlockRecovery,
		OperationsWorkflowBlockVerification,
		OperationsWorkflowBlockAudit,
	}
	if len(evidence.BlockReasons) != len(want) {
		t.Fatalf("block reasons = %#v, want %#v", evidence.BlockReasons, want)
	}
	for i := range want {
		if evidence.BlockReasons[i] != want[i] {
			t.Fatalf("block reasons = %#v, want %#v", evidence.BlockReasons, want)
		}
	}
}

func TestBuildOperationsWorkflowEvidenceRejectsFutureAndAuthorizingRecovery(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 45, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	verifiedAt := now.Add(-5 * time.Minute)
	recoveryEvidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		digest,
		RecoveryPathObservation{
			RecoveryPointID:        "rp-31",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            1,
			VerifiedBackupCount:    1,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	base := OperationsWorkflowEvidenceInput{
		Approval: ApprovalEvidence{
			ContractVersion: ApprovalEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			State:           ApprovalEvidenceSatisfied,
			Satisfied:       true,
			ObservedAt:      now.Add(-8 * time.Minute),
		},
		Result: JobResultEvidence{
			ContractVersion: JobResultEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			JobID:           "job-31",
			JobVersion:      7,
			Outcome:         job.StatusSucceeded,
			OutputPresent:   true,
			OutputDigest:    "sha256:" + strings.Repeat("b", 64),
			HealthChecks:    1,
			AuditEvents:     1,
			WorstHealth:     events.HealthHealthy,
			ObservedAt:      now.Add(-2 * time.Minute),
		},
		Recovery:   recoveryEvidence,
		ObservedAt: now,
	}

	future := base
	future.Result.ObservedAt = now.Add(time.Second)
	if _, err := BuildOperationsWorkflowEvidence(future); !errors.Is(err, ErrInvalidOperationsWorkflowEvidence) {
		t.Fatalf("future component evidence must fail closed, got %v", err)
	}

	authorizing := base
	authorizing.Recovery.ExecutionAuthorized = true
	if _, err := BuildOperationsWorkflowEvidence(authorizing); !errors.Is(err, ErrInvalidOperationsWorkflowEvidence) {
		t.Fatalf("authorizing recovery evidence must fail closed, got %v", err)
	}
}

func TestBuildOperationsWorkflowEvidenceCompleteDoesNotMeanSuccessfulOutcome(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 45, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	verifiedAt := now.Add(-5 * time.Minute)
	recoveryEvidence, err := BuildRecoveryPathEvidence(
		"change-31",
		"revision-31",
		digest,
		RecoveryPathObservation{
			RecoveryPointID:        "rp-31",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            1,
			VerifiedBackupCount:    1,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	evidence, err := BuildOperationsWorkflowEvidence(OperationsWorkflowEvidenceInput{
		Approval: ApprovalEvidence{
			ContractVersion: ApprovalEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			State:           ApprovalEvidenceSatisfied,
			Satisfied:       true,
			ObservedAt:      now.Add(-8 * time.Minute),
		},
		Result: JobResultEvidence{
			ContractVersion: JobResultEvidenceContractVersion,
			ChangeID:        "change-31",
			RevisionID:      "revision-31",
			RevisionDigest:  digest,
			JobID:           "job-31",
			JobVersion:      7,
			Outcome:         job.StatusFailed,
			OutputPresent:   true,
			OutputDigest:    "sha256:" + strings.Repeat("b", 64),
			AuditEvents:     1,
			ObservedAt:      now.Add(-2 * time.Minute),
		},
		Recovery:   recoveryEvidence,
		ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != OperationsWorkflowEvidenceComplete || !evidence.EvidenceComplete {
		t.Fatalf("complete failed-operation evidence chain should remain complete: %#v", evidence)
	}
	if evidence.Outcome != job.StatusFailed {
		t.Fatalf("failed outcome was obscured: %#v", evidence)
	}
}
