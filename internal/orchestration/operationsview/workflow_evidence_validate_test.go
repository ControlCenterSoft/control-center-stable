package operationsview

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

func TestValidateOperationsWorkflowEvidenceAcceptsCanonicalCompleteRecord(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 55, 0, 0, time.UTC)
	evidence := OperationsWorkflowEvidence{
		ContractVersion:           OperationsWorkflowEvidenceContractVersion,
		ChangeID:                  "change-31",
		RevisionID:                "revision-31",
		RevisionDigest:            "sha256:" + strings.Repeat("a", 64),
		ApprovalState:             ApprovalEvidenceSatisfied,
		ApprovalSatisfied:         true,
		ApprovalObservedAt:        now.Add(-8 * time.Minute),
		JobID:                     "job-31",
		JobVersion:                7,
		Outcome:                   job.StatusSucceeded,
		ResultOutputPresent:       true,
		ResultOutputDigest:        "sha256:" + strings.Repeat("b", 64),
		HealthChecks:              1,
		AuditEvents:               1,
		WorstHealth:               events.HealthHealthy,
		ResultObservedAt:          now.Add(-2 * time.Minute),
		RecoveryPointID:           "rp-31",
		RecoveryState:             RecoveryPathReady,
		RecoveryEvaluatedAt:       now.Add(-4 * time.Minute),
		State:                     OperationsWorkflowEvidenceComplete,
		BlockReasons:              []OperationsWorkflowBlockReason{},
		EvidenceComplete:          true,
		ObservedAt:                now,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	if err := ValidateOperationsWorkflowEvidence(evidence); err != nil {
		t.Fatalf("canonical evidence rejected: %v", err)
	}
}

func TestValidateOperationsWorkflowEvidenceRejectsFalseSuccessAndAuthority(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 55, 0, 0, time.UTC)
	base := OperationsWorkflowEvidence{
		ContractVersion:           OperationsWorkflowEvidenceContractVersion,
		ChangeID:                  "change-31",
		RevisionID:                "revision-31",
		RevisionDigest:            "sha256:" + strings.Repeat("a", 64),
		ApprovalState:             ApprovalEvidenceSatisfied,
		ApprovalSatisfied:         true,
		ApprovalObservedAt:        now.Add(-8 * time.Minute),
		JobID:                     "job-31",
		JobVersion:                7,
		Outcome:                   job.StatusSucceeded,
		ResultOutputPresent:       true,
		ResultOutputDigest:        "sha256:" + strings.Repeat("b", 64),
		HealthChecks:              1,
		AuditEvents:               1,
		WorstHealth:               events.HealthHealthy,
		ResultObservedAt:          now.Add(-2 * time.Minute),
		RecoveryPointID:           "rp-31",
		RecoveryState:             RecoveryPathReady,
		RecoveryEvaluatedAt:       now.Add(-4 * time.Minute),
		State:                     OperationsWorkflowEvidenceComplete,
		BlockReasons:              []OperationsWorkflowBlockReason{},
		EvidenceComplete:          true,
		ObservedAt:                now,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}

	falseSuccess := base
	falseSuccess.WorstHealth = events.HealthDegraded
	if err := ValidateOperationsWorkflowEvidence(falseSuccess); !errors.Is(err, ErrInvalidOperationsWorkflowEvidence) {
		t.Fatalf("degraded result presented as complete must fail closed, got %v", err)
	}

	authorizing := base
	authorizing.ExecutionAuthorized = true
	if err := ValidateOperationsWorkflowEvidence(authorizing); !errors.Is(err, ErrInvalidOperationsWorkflowEvidence) {
		t.Fatalf("authorizing workflow evidence must fail closed, got %v", err)
	}
}

func TestValidateOperationsWorkflowEvidenceAcceptsCompleteFailureEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 55, 0, 0, time.UTC)
	evidence := OperationsWorkflowEvidence{
		ContractVersion:           OperationsWorkflowEvidenceContractVersion,
		ChangeID:                  "change-31",
		RevisionID:                "revision-31",
		RevisionDigest:            "sha256:" + strings.Repeat("a", 64),
		ApprovalState:             ApprovalEvidenceSatisfied,
		ApprovalSatisfied:         true,
		ApprovalObservedAt:        now.Add(-8 * time.Minute),
		JobID:                     "job-31",
		JobVersion:                7,
		Outcome:                   job.StatusFailed,
		ResultOutputPresent:       true,
		ResultOutputDigest:        "sha256:" + strings.Repeat("b", 64),
		HealthChecks:              0,
		AuditEvents:               1,
		ResultObservedAt:          now.Add(-2 * time.Minute),
		RecoveryPointID:           "rp-31",
		RecoveryState:             RecoveryPathReady,
		RecoveryEvaluatedAt:       now.Add(-4 * time.Minute),
		State:                     OperationsWorkflowEvidenceComplete,
		BlockReasons:              []OperationsWorkflowBlockReason{},
		EvidenceComplete:          true,
		ObservedAt:                now,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	if err := ValidateOperationsWorkflowEvidence(evidence); err != nil {
		t.Fatalf("complete failure evidence must validate without becoming success: %v", err)
	}
}
