package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/operationsview"
	"control-center/internal/recovery"
)

func TestOperationsWorkflowEvidenceEndToEndFromPersistedSourcesToOperatorView(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 30, 0, 0, time.UTC)
	digest := workflowTestDigest("a")

	changeSnapshot := validChangeSnapshot("change-a", "service.ensure", now.Add(-4*time.Minute))
	changeSnapshot.State = change.StateSucceeded

	sourceJob := validJob("job-a", "change-a", "service.ensure", now.Add(-4*time.Minute))
	sourceJob.Status = job.StatusSucceeded
	sourceJob.Attempt = 1
	sourceJob.Version = 7
	sourceJob.CreatedAt = now.Add(-10 * time.Minute)
	sourceJob.UpdatedAt = now.Add(-4 * time.Minute)
	sourceJob.Output = &events.Output{
		Health: []events.Health{{
			ResourceID: "resource-a",
			Status:     events.HealthHealthy,
			CheckedAt:  now.Add(-5 * time.Minute),
		}},
		AuditEvents: []events.AuditEvent{{
			ID:            "audit-a",
			OccurredAt:    now.Add(-4 * time.Minute),
			Actor:         "system",
			Action:        "service.ensure",
			Outcome:       "succeeded",
			CorrelationID: "corr-a",
		}},
	}

	approvalEvidence, err := operationsview.BuildApprovalEvidence(operationsview.ApprovalEvidenceInput{
		Change:         changeSnapshot,
		RevisionDigest: digest,
		ObservedAt:     now.Add(-3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	resultEvidence, err := operationsview.BuildJobResultEvidence(operationsview.JobResultEvidenceInput{
		Change:         changeSnapshot,
		Job:            sourceJob,
		RevisionDigest: digest,
		ObservedAt:     now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := now.Add(-5 * time.Minute)
	recoveryEvidence, err := operationsview.BuildRecoveryPathEvidence(
		"change-a",
		"revision-a",
		digest,
		operationsview.RecoveryPathObservation{
			RecoveryPointID:        "rp-a",
			RecoveryPointState:     recovery.RecoveryPointReady,
			BackupCount:            1,
			VerifiedBackupCount:    1,
			VerificationOutcome:    recovery.VerificationPassed,
			VerificationObservedAt: &verifiedAt,
		},
		now.Add(-3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	workflowEvidence, err := operationsview.BuildOperationsWorkflowEvidence(operationsview.OperationsWorkflowEvidenceInput{
		Approval:   approvalEvidence,
		Result:     resultEvidence,
		Recovery:   recoveryEvidence,
		ObservedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if workflowEvidence.State != operationsview.OperationsWorkflowEvidenceComplete || !workflowEvidence.EvidenceComplete {
		t.Fatalf("source evidence chain is not complete: %#v", workflowEvidence)
	}

	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{changeSnapshot},
		Jobs:          []job.Job{sourceJob},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := ApplyVerifiedOperationsWorkflowEvidence(
		view,
		map[string]string{"revision-a": digest},
		[]operationsview.OperationsWorkflowEvidence{workflowEvidence},
		true,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	summary := enriched.Changes[0].WorkflowEvidence
	if summary.Availability != EvidenceAvailable || summary.State != operationsview.OperationsWorkflowEvidenceComplete || !summary.EvidenceComplete || summary.Outcome != job.StatusSucceeded {
		t.Fatalf("end-to-end workflow evidence not exposed safely: %#v", summary)
	}
}

func TestApplyVerifiedOperationsWorkflowEvidenceProjectsExactCurrentJob(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 20, 0, 0, time.UTC)
	view := workflowTestView(t, now, job.StatusSucceeded, 7)
	digest := workflowTestDigest("a")
	evidence := workflowTestEvidence(now, digest, operationsview.OperationsWorkflowEvidenceComplete, job.StatusSucceeded, 7)

	enriched, err := ApplyVerifiedOperationsWorkflowEvidence(
		view,
		map[string]string{"revision-a": digest},
		[]operationsview.OperationsWorkflowEvidence{evidence},
		true,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	summary := enriched.Changes[0].WorkflowEvidence
	if summary.Availability != EvidenceAvailable || summary.State != operationsview.OperationsWorkflowEvidenceComplete || !summary.EvidenceComplete {
		t.Fatalf("workflow summary not projected: %#v", summary)
	}
	if summary.JobID != "job-a" || summary.JobVersion != 7 || summary.Outcome != job.StatusSucceeded || len(summary.BlockReasons) != 0 {
		t.Fatalf("workflow identity/result projection mismatch: %#v", summary)
	}
	if view.Changes[0].WorkflowEvidence.Availability != EvidenceUnavailable {
		t.Fatalf("source view was mutated: %#v", view.Changes[0].WorkflowEvidence)
	}
}

func TestApplyVerifiedOperationsWorkflowEvidencePreservesBlockedAndFailedSemantics(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 20, 0, 0, time.UTC)
	digest := workflowTestDigest("a")

	blockedView := workflowTestView(t, now, job.StatusSucceeded, 7)
	blocked := workflowTestEvidence(now, digest, operationsview.OperationsWorkflowEvidenceBlocked, job.StatusSucceeded, 7)
	blocked.RecoveryState = operationsview.RecoveryPathBlocked
	blocked.BlockReasons = []operationsview.OperationsWorkflowBlockReason{operationsview.OperationsWorkflowBlockRecovery}
	blocked.EvidenceComplete = false
	enriched, err := ApplyVerifiedOperationsWorkflowEvidence(blockedView, map[string]string{"revision-a": digest}, []operationsview.OperationsWorkflowEvidence{blocked}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Changes[0].WorkflowEvidence.State != operationsview.OperationsWorkflowEvidenceBlocked || enriched.Changes[0].WorkflowEvidence.EvidenceComplete {
		t.Fatalf("blocked evidence was upgraded: %#v", enriched.Changes[0].WorkflowEvidence)
	}

	failedView := workflowTestView(t, now, job.StatusFailed, 7)
	failed := workflowTestEvidence(now, digest, operationsview.OperationsWorkflowEvidenceComplete, job.StatusFailed, 7)
	failed.HealthChecks = 0
	failed.WorstHealth = ""
	failedView.Changes[0].Jobs[0].Output.HealthChecks = 0
	failedView.Changes[0].Jobs[0].Output.WorstHealth = ""
	enriched, err = ApplyVerifiedOperationsWorkflowEvidence(failedView, map[string]string{"revision-a": digest}, []operationsview.OperationsWorkflowEvidence{failed}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Changes[0].WorkflowEvidence.Outcome != job.StatusFailed || !enriched.Changes[0].WorkflowEvidence.EvidenceComplete {
		t.Fatalf("complete evidence obscured failed outcome: %#v", enriched.Changes[0].WorkflowEvidence)
	}
}

func TestApplyVerifiedOperationsWorkflowEvidenceRejectsStaleOrContradictoryProjection(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 20, 0, 0, time.UTC)
	digest := workflowTestDigest("a")
	view := workflowTestView(t, now, job.StatusSucceeded, 7)
	base := workflowTestEvidence(now, digest, operationsview.OperationsWorkflowEvidenceComplete, job.StatusSucceeded, 7)

	cases := []struct {
		name   string
		mutate func(*operationsview.OperationsWorkflowEvidence)
	}{
		{name: "revision digest", mutate: func(e *operationsview.OperationsWorkflowEvidence) { e.RevisionDigest = workflowTestDigest("c") }},
		{name: "job version", mutate: func(e *operationsview.OperationsWorkflowEvidence) { e.JobVersion = 8 }},
		{name: "job outcome", mutate: func(e *operationsview.OperationsWorkflowEvidence) { e.Outcome = job.StatusFailed }},
		{name: "result summary", mutate: func(e *operationsview.OperationsWorkflowEvidence) { e.AuditEvents = 2 }},
		{name: "approval summary", mutate: func(e *operationsview.OperationsWorkflowEvidence) {
			e.ApprovalState = operationsview.ApprovalEvidencePending
			e.ApprovalSatisfied = false
			e.State = operationsview.OperationsWorkflowEvidenceBlocked
			e.BlockReasons = []operationsview.OperationsWorkflowBlockReason{operationsview.OperationsWorkflowBlockApproval}
			e.EvidenceComplete = false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence := base
			evidence.BlockReasons = append([]operationsview.OperationsWorkflowBlockReason(nil), base.BlockReasons...)
			tc.mutate(&evidence)
			if _, err := ApplyVerifiedOperationsWorkflowEvidence(view, map[string]string{"revision-a": digest}, []operationsview.OperationsWorkflowEvidence{evidence}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
				t.Fatalf("contradictory workflow evidence must fail closed, got %v", err)
			}
		})
	}
}

func TestApplyVerifiedOperationsWorkflowEvidenceFailsClosedWhenSourceUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 20, 0, 0, time.UTC)
	view := workflowTestView(t, now, job.StatusSucceeded, 7)
	view.Changes[0].WorkflowEvidence = WorkflowEvidenceSummary{
		Availability:     EvidenceAvailable,
		State:            operationsview.OperationsWorkflowEvidenceComplete,
		EvidenceComplete: true,
		JobID:            "job-a",
		JobVersion:       7,
		Outcome:          job.StatusSucceeded,
		BlockReasons:     []operationsview.OperationsWorkflowBlockReason{},
		ObservedAt:       ptrWorkflowTime(now.Add(-time.Minute)),
	}
	closed, err := ApplyVerifiedOperationsWorkflowEvidence(view, nil, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Changes[0].WorkflowEvidence.Availability != EvidenceUnavailable {
		t.Fatalf("unloaded workflow source must project unavailable: %#v", closed.Changes[0].WorkflowEvidence)
	}

	digest := workflowTestDigest("a")
	evidence := workflowTestEvidence(now, digest, operationsview.OperationsWorkflowEvidenceComplete, job.StatusSucceeded, 7)
	if _, err := ApplyVerifiedOperationsWorkflowEvidence(view, map[string]string{"revision-a": digest}, []operationsview.OperationsWorkflowEvidence{evidence}, false, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("supplied evidence from unloaded source must fail closed, got %v", err)
	}
}

func TestApplyVerifiedOperationsWorkflowEvidenceRejectsDuplicatePerChange(t *testing.T) {
	now := time.Date(2026, 9, 12, 6, 20, 0, 0, time.UTC)
	digest := workflowTestDigest("a")
	view := workflowTestView(t, now, job.StatusSucceeded, 7)
	evidence := workflowTestEvidence(now, digest, operationsview.OperationsWorkflowEvidenceComplete, job.StatusSucceeded, 7)
	if _, err := ApplyVerifiedOperationsWorkflowEvidence(view, map[string]string{"revision-a": digest}, []operationsview.OperationsWorkflowEvidence{evidence, evidence}, true, now); !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("duplicate workflow evidence must fail closed, got %v", err)
	}
}

func workflowTestView(t *testing.T, now time.Time, status job.Status, version uint64) ChangesJobsView {
	t.Helper()
	sourceJob := validJob("job-a", "change-a", "service.ensure", now.Add(-2*time.Minute))
	sourceJob.Status = status
	sourceJob.Version = version
	sourceJob.Attempt = 1
	sourceJob.Output = &events.Output{
		Health:      []events.Health{{Status: events.HealthHealthy, CheckedAt: now.Add(-3 * time.Minute)}},
		AuditEvents: []events.AuditEvent{{ID: "audit-a", OccurredAt: now.Add(-2 * time.Minute)}},
	}
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))},
		Jobs:          []job.Job{sourceJob},
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func workflowTestEvidence(now time.Time, digest string, state operationsview.OperationsWorkflowEvidenceState, outcome job.Status, version uint64) operationsview.OperationsWorkflowEvidence {
	return operationsview.OperationsWorkflowEvidence{
		ContractVersion:           operationsview.OperationsWorkflowEvidenceContractVersion,
		ChangeID:                  "change-a",
		RevisionID:                "revision-a",
		RevisionDigest:            digest,
		ApprovalState:             operationsview.ApprovalEvidenceSatisfied,
		ApprovalSatisfied:         true,
		ApprovalObservedAt:        now.Add(-8 * time.Minute),
		JobID:                     "job-a",
		JobVersion:                version,
		Outcome:                   outcome,
		ResultOutputPresent:       true,
		ResultOutputDigest:        workflowTestDigest("b"),
		HealthChecks:              1,
		AuditEvents:               1,
		WorstHealth:               events.HealthHealthy,
		ResultObservedAt:          now.Add(-2 * time.Minute),
		RecoveryPointID:           "rp-a",
		RecoveryState:             operationsview.RecoveryPathReady,
		RecoveryEvaluatedAt:       now.Add(-4 * time.Minute),
		State:                     state,
		BlockReasons:              []operationsview.OperationsWorkflowBlockReason{},
		EvidenceComplete:          state == operationsview.OperationsWorkflowEvidenceComplete,
		ObservedAt:                now.Add(-time.Minute),
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
}

func workflowTestDigest(ch string) string {
	return "sha256:" + strings.Repeat(ch, 64)
}

func ptrWorkflowTime(value time.Time) *time.Time {
	return &value
}
