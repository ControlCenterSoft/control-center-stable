package operationsview

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
)

const (
	OperationsWorkflowEvidenceContractVersion     = "ui.operations-workflow-evidence/v1"
	MaxOperationsWorkflowEvidenceIdentifierLength = 255
)

var ErrInvalidOperationsWorkflowEvidence = errors.New("invalid operations workflow evidence")

type OperationsWorkflowEvidenceState string

const (
	OperationsWorkflowEvidenceComplete OperationsWorkflowEvidenceState = "complete"
	OperationsWorkflowEvidenceBlocked  OperationsWorkflowEvidenceState = "blocked"
)

type OperationsWorkflowBlockReason string

const (
	OperationsWorkflowBlockApproval     OperationsWorkflowBlockReason = "approval_not_satisfied"
	OperationsWorkflowBlockRecovery     OperationsWorkflowBlockReason = "recovery_not_ready"
	OperationsWorkflowBlockVerification OperationsWorkflowBlockReason = "post_condition_not_verified"
	OperationsWorkflowBlockAudit        OperationsWorkflowBlockReason = "audit_evidence_missing"
)

// OperationsWorkflowEvidence binds the already-built approval, terminal Job
// result and recovery-path projections for one exact immutable Change revision.
// "complete" means only that the evidence chain is internally consistent and
// contains the required safety evidence; it never means that the operation
// itself succeeded and it never grants execution authority.
type OperationsWorkflowEvidence struct {
	ContractVersion           string                          `json:"contract_version"`
	ChangeID                  string                          `json:"change_id"`
	RevisionID                string                          `json:"revision_id"`
	RevisionDigest            string                          `json:"revision_digest"`
	ApprovalState             ApprovalEvidenceState           `json:"approval_state"`
	ApprovalSatisfied         bool                            `json:"approval_satisfied"`
	ApprovalObservedAt        time.Time                       `json:"approval_observed_at"`
	JobID                     string                          `json:"job_id"`
	JobVersion                uint64                          `json:"job_version"`
	Outcome                   job.Status                      `json:"outcome"`
	ResultOutputPresent       bool                            `json:"result_output_present"`
	ResultOutputDigest        string                          `json:"result_output_digest,omitempty"`
	HealthChecks              int                             `json:"health_checks"`
	AuditEvents               int                             `json:"audit_events"`
	WorstHealth               events.HealthStatus             `json:"worst_health,omitempty"`
	ResultObservedAt          time.Time                       `json:"result_observed_at"`
	RecoveryPointID           string                          `json:"recovery_point_id"`
	RecoveryState             RecoveryPathState               `json:"recovery_state"`
	RecoveryEvaluatedAt       time.Time                       `json:"recovery_evaluated_at"`
	State                     OperationsWorkflowEvidenceState `json:"state"`
	BlockReasons              []OperationsWorkflowBlockReason `json:"block_reasons"`
	EvidenceComplete          bool                            `json:"evidence_complete"`
	ObservedAt                time.Time                       `json:"observed_at"`
	ExecutionAuthorized       bool                            `json:"execution_authorized"`
	ProductionMutationAllowed bool                            `json:"production_mutation_allowed"`
}

type OperationsWorkflowEvidenceInput struct {
	Approval   ApprovalEvidence
	Result     JobResultEvidence
	Recovery   RecoveryPathEvidence
	ObservedAt time.Time
}

// BuildOperationsWorkflowEvidence performs the cross-contract checks that are
// otherwise easy to miss when the operator UI receives the three evidence
// projections independently. Legitimate incomplete safety evidence is reported
// as a bounded blocked state; identity/digest/timestamp contradictions are
// rejected fail-closed.
func BuildOperationsWorkflowEvidence(input OperationsWorkflowEvidenceInput) (OperationsWorkflowEvidence, error) {
	if input.ObservedAt.IsZero() {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("observed_at is required")
	}
	observedAt := input.ObservedAt.UTC()

	approval := input.Approval
	result := input.Result
	recovery := input.Recovery

	if approval.ContractVersion != ApprovalEvidenceContractVersion {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("approval contract_version is invalid")
	}
	if result.ContractVersion != JobResultEvidenceContractVersion {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("result contract_version is invalid")
	}
	if err := ValidateRecoveryPathEvidence(recovery); err != nil {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("recovery evidence is invalid")
	}

	if err := validateOperationsWorkflowIdentifier("change_id", approval.ChangeID); err != nil {
		return OperationsWorkflowEvidence{}, err
	}
	if err := validateOperationsWorkflowIdentifier("revision_id", approval.RevisionID); err != nil {
		return OperationsWorkflowEvidence{}, err
	}
	if err := validateOperationsWorkflowIdentifier("job_id", result.JobID); err != nil {
		return OperationsWorkflowEvidence{}, err
	}
	if err := validateOperationsWorkflowIdentifier("recovery_point_id", recovery.RecoveryPointID); err != nil {
		return OperationsWorkflowEvidence{}, err
	}
	if !validOperationsWorkflowDigest(approval.RevisionDigest) {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("revision_digest is invalid")
	}
	if result.JobVersion == 0 {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("job_version is required")
	}
	if !result.Outcome.Terminal() {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("result outcome is not terminal")
	}
	if err := validateOperationsWorkflowApproval(approval.State, approval.Satisfied); err != nil {
		return OperationsWorkflowEvidence{}, err
	}
	if err := validateOperationsWorkflowResultSummary(result.OutputPresent, result.OutputDigest, result.HealthChecks, result.AuditEvents, result.WorstHealth, result.Outcome); err != nil {
		return OperationsWorkflowEvidence{}, err
	}
	if approval.ObservedAt.IsZero() || result.ObservedAt.IsZero() || recovery.EvaluatedAt.IsZero() {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("component observation time is required")
	}
	if approval.ObservedAt.After(observedAt) || result.ObservedAt.After(observedAt) || recovery.EvaluatedAt.After(observedAt) {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("component evidence is newer than workflow observation")
	}
	if approval.ObservedAt.After(result.ObservedAt) {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("approval evidence is newer than terminal result evidence")
	}

	if approval.ChangeID != result.ChangeID || approval.ChangeID != recovery.ChangeID {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("change_id mismatch across evidence")
	}
	if approval.RevisionID != result.RevisionID || approval.RevisionID != recovery.RevisionID {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("revision_id mismatch across evidence")
	}
	if approval.RevisionDigest != result.RevisionDigest || approval.RevisionDigest != recovery.RevisionDigest {
		return OperationsWorkflowEvidence{}, invalidOperationsWorkflow("revision_digest mismatch across evidence")
	}

	blockReasons := operationsWorkflowBlockReasons(
		approval.State,
		approval.Satisfied,
		recovery.State,
		result.Outcome,
		result.HealthChecks,
		result.WorstHealth,
		result.AuditEvents,
	)
	state := OperationsWorkflowEvidenceComplete
	if len(blockReasons) != 0 {
		state = OperationsWorkflowEvidenceBlocked
	}

	return OperationsWorkflowEvidence{
		ContractVersion:           OperationsWorkflowEvidenceContractVersion,
		ChangeID:                  approval.ChangeID,
		RevisionID:                approval.RevisionID,
		RevisionDigest:            approval.RevisionDigest,
		ApprovalState:             approval.State,
		ApprovalSatisfied:         approval.Satisfied,
		ApprovalObservedAt:        approval.ObservedAt.UTC(),
		JobID:                     result.JobID,
		JobVersion:                result.JobVersion,
		Outcome:                   result.Outcome,
		ResultOutputPresent:       result.OutputPresent,
		ResultOutputDigest:        result.OutputDigest,
		HealthChecks:              result.HealthChecks,
		AuditEvents:               result.AuditEvents,
		WorstHealth:               result.WorstHealth,
		ResultObservedAt:          result.ObservedAt.UTC(),
		RecoveryPointID:           recovery.RecoveryPointID,
		RecoveryState:             recovery.State,
		RecoveryEvaluatedAt:       recovery.EvaluatedAt.UTC(),
		State:                     state,
		BlockReasons:              blockReasons,
		EvidenceComplete:          state == OperationsWorkflowEvidenceComplete,
		ObservedAt:                observedAt,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}, nil
}

// ValidateOperationsWorkflowEvidence is the strict storage/transport consumer
// boundary. It re-derives every bounded status from the serialized summaries and
// rejects attempts to upgrade incomplete evidence or failure outcomes into
// execution authority or a false-success state.
func ValidateOperationsWorkflowEvidence(evidence OperationsWorkflowEvidence) error {
	if evidence.ContractVersion != OperationsWorkflowEvidenceContractVersion {
		return invalidOperationsWorkflow("contract_version is invalid")
	}
	if evidence.ExecutionAuthorized || evidence.ProductionMutationAllowed {
		return invalidOperationsWorkflow("workflow evidence must not grant execution authority")
	}
	for name, value := range map[string]string{
		"change_id": evidence.ChangeID, "revision_id": evidence.RevisionID,
		"job_id": evidence.JobID, "recovery_point_id": evidence.RecoveryPointID,
	} {
		if err := validateOperationsWorkflowIdentifier(name, value); err != nil {
			return err
		}
	}
	if !validOperationsWorkflowDigest(evidence.RevisionDigest) {
		return invalidOperationsWorkflow("revision_digest is invalid")
	}
	if evidence.JobVersion == 0 || !evidence.Outcome.Terminal() {
		return invalidOperationsWorkflow("terminal job identity is invalid")
	}
	if err := validateOperationsWorkflowApproval(evidence.ApprovalState, evidence.ApprovalSatisfied); err != nil {
		return err
	}
	if err := validateOperationsWorkflowResultSummary(
		evidence.ResultOutputPresent,
		evidence.ResultOutputDigest,
		evidence.HealthChecks,
		evidence.AuditEvents,
		evidence.WorstHealth,
		evidence.Outcome,
	); err != nil {
		return err
	}
	if evidence.ApprovalObservedAt.IsZero() || evidence.ResultObservedAt.IsZero() || evidence.RecoveryEvaluatedAt.IsZero() || evidence.ObservedAt.IsZero() {
		return invalidOperationsWorkflow("evidence timestamps are required")
	}
	if evidence.ApprovalObservedAt.After(evidence.ObservedAt) || evidence.ResultObservedAt.After(evidence.ObservedAt) || evidence.RecoveryEvaluatedAt.After(evidence.ObservedAt) {
		return invalidOperationsWorkflow("component evidence is newer than workflow observation")
	}
	if evidence.ApprovalObservedAt.After(evidence.ResultObservedAt) {
		return invalidOperationsWorkflow("approval evidence is newer than terminal result evidence")
	}
	if !validOperationsWorkflowRecoveryState(evidence.RecoveryState) {
		return invalidOperationsWorkflow("recovery_state is invalid")
	}

	wantReasons := operationsWorkflowBlockReasons(
		evidence.ApprovalState,
		evidence.ApprovalSatisfied,
		evidence.RecoveryState,
		evidence.Outcome,
		evidence.HealthChecks,
		evidence.WorstHealth,
		evidence.AuditEvents,
	)
	if len(evidence.BlockReasons) != len(wantReasons) {
		return invalidOperationsWorkflow("block_reasons are inconsistent with component evidence")
	}
	for i := range wantReasons {
		if evidence.BlockReasons[i] != wantReasons[i] {
			return invalidOperationsWorkflow("block_reasons are inconsistent with component evidence")
		}
	}
	wantState := OperationsWorkflowEvidenceComplete
	if len(wantReasons) != 0 {
		wantState = OperationsWorkflowEvidenceBlocked
	}
	if evidence.State != wantState || evidence.EvidenceComplete != (wantState == OperationsWorkflowEvidenceComplete) {
		return invalidOperationsWorkflow("state is inconsistent with component evidence")
	}
	return nil
}

func operationsWorkflowBlockReasons(
	approvalState ApprovalEvidenceState,
	approvalSatisfied bool,
	recoveryState RecoveryPathState,
	outcome job.Status,
	healthChecks int,
	worstHealth events.HealthStatus,
	auditEvents int,
) []OperationsWorkflowBlockReason {
	blockReasons := make([]OperationsWorkflowBlockReason, 0, 4)
	if !approvalSatisfied || (approvalState != ApprovalEvidenceSatisfied && approvalState != ApprovalEvidenceNotRequired) {
		blockReasons = append(blockReasons, OperationsWorkflowBlockApproval)
	}
	if recoveryState != RecoveryPathReady {
		blockReasons = append(blockReasons, OperationsWorkflowBlockRecovery)
	}
	if outcome == job.StatusSucceeded && (healthChecks == 0 || worstHealth != events.HealthHealthy) {
		blockReasons = append(blockReasons, OperationsWorkflowBlockVerification)
	}
	if auditEvents == 0 {
		blockReasons = append(blockReasons, OperationsWorkflowBlockAudit)
	}
	return blockReasons
}

func validateOperationsWorkflowApproval(state ApprovalEvidenceState, satisfied bool) error {
	switch state {
	case ApprovalEvidenceSatisfied, ApprovalEvidenceNotRequired:
		if !satisfied {
			return invalidOperationsWorkflow("approval state is satisfied but satisfied=false")
		}
	case ApprovalEvidencePending, ApprovalEvidenceDenied:
		if satisfied {
			return invalidOperationsWorkflow("approval state is not satisfied but satisfied=true")
		}
	default:
		return invalidOperationsWorkflow("approval_state is invalid")
	}
	return nil
}

func validateOperationsWorkflowResultSummary(outputPresent bool, outputDigest string, healthChecks, auditEvents int, worstHealth events.HealthStatus, outcome job.Status) error {
	if healthChecks < 0 || auditEvents < 0 {
		return invalidOperationsWorkflow("result evidence counts must be non-negative")
	}
	if outputPresent {
		if !validOperationsWorkflowDigest(outputDigest) {
			return invalidOperationsWorkflow("result output_digest is invalid")
		}
	} else {
		if outputDigest != "" {
			return invalidOperationsWorkflow("result output_digest requires output_present")
		}
		if outcome != job.StatusCancelled {
			return invalidOperationsWorkflow("terminal non-cancelled Job is missing result output")
		}
	}
	if healthChecks == 0 {
		if worstHealth != "" {
			return invalidOperationsWorkflow("worst_health requires health evidence")
		}
		return nil
	}
	if !validOperationsWorkflowHealth(worstHealth) {
		return invalidOperationsWorkflow("worst_health is invalid")
	}
	return nil
}

func validOperationsWorkflowHealth(value events.HealthStatus) bool {
	switch value {
	case events.HealthHealthy, events.HealthDegraded, events.HealthFailed, events.HealthUnknown:
		return true
	default:
		return false
	}
}

func validOperationsWorkflowRecoveryState(value RecoveryPathState) bool {
	switch value {
	case RecoveryPathReady, RecoveryPathBlocked, RecoveryPathExpired:
		return true
	default:
		return false
	}
}

func validateOperationsWorkflowIdentifier(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(value) > MaxOperationsWorkflowEvidenceIdentifierLength {
		return invalidOperationsWorkflow("canonical " + name + " is required")
	}
	return nil
}

func validOperationsWorkflowDigest(value string) bool {
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

func invalidOperationsWorkflow(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidOperationsWorkflowEvidence, message)
}
