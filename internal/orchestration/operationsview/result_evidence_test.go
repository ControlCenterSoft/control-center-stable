package operationsview

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

func TestBuildJobResultEvidenceBindsTerminalOutputWithoutProjectingSensitiveDetails(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 30, 0, 0, time.UTC)
	input := validJobResultEvidenceInput(now)

	got, err := BuildJobResultEvidence(input)
	if err != nil {
		t.Fatalf("BuildJobResultEvidence() error = %v", err)
	}
	if got.ContractVersion != JobResultEvidenceContractVersion || got.ChangeID != "chg-1" || got.JobID != "job-1" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	if got.Outcome != job.StatusSucceeded || !got.OutputPresent || got.OutputDigest == "" {
		t.Fatalf("unexpected outcome evidence: %#v", got)
	}
	if got.ActualStates != 1 || got.HealthChecks != 2 || got.AuditEvents != 1 || got.WorstHealth != events.HealthDegraded {
		t.Fatalf("unexpected summary: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	for _, secret := range []string{"super-secret", "backend-only-health-message", "raw-audit-detail"} {
		if string(encoded) == "" || containsResultEvidenceText(encoded, secret) {
			t.Fatalf("sensitive source detail leaked into projection: %q", encoded)
		}
	}
}

func TestBuildJobResultEvidenceDigestIsOrderIndependent(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 30, 0, 0, time.UTC)
	left := validJobResultEvidenceInput(now)
	right := validJobResultEvidenceInput(now)
	right.Job.Output.Health[0], right.Job.Output.Health[1] = right.Job.Output.Health[1], right.Job.Output.Health[0]

	first, err := BuildJobResultEvidence(left)
	if err != nil {
		t.Fatalf("first evidence: %v", err)
	}
	second, err := BuildJobResultEvidence(right)
	if err != nil {
		t.Fatalf("second evidence: %v", err)
	}
	if first.OutputDigest != second.OutputDigest {
		t.Fatalf("digest changed with source ordering: %q != %q", first.OutputDigest, second.OutputDigest)
	}
}

func TestBuildJobResultEvidenceRejectsMismatchedOrNonTerminalState(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 30, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*JobResultEvidenceInput)
	}{
		{name: "job still running", mutate: func(in *JobResultEvidenceInput) {
			in.Job.Status = job.StatusRunning
			in.Change.State = change.StateExecuting
		}},
		{name: "terminal mismatch", mutate: func(in *JobResultEvidenceInput) { in.Change.State = change.StateFailed }},
		{name: "wrong change binding", mutate: func(in *JobResultEvidenceInput) { in.Job.ChangeID = "chg-other" }},
		{name: "future output", mutate: func(in *JobResultEvidenceInput) { in.Job.Output.Health[0].CheckedAt = now.Add(time.Minute) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := validJobResultEvidenceInput(now)
			tc.mutate(&input)
			if _, err := BuildJobResultEvidence(input); !errors.Is(err, ErrInvalidJobResultEvidence) {
				t.Fatalf("error = %v, want ErrInvalidJobResultEvidence", err)
			}
		})
	}
}

func TestBuildJobResultEvidenceAllowsCancelledJobWithoutOutput(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 30, 0, 0, time.UTC)
	input := validJobResultEvidenceInput(now)
	input.Job.Status = job.StatusCancelled
	input.Job.Output = nil
	input.Change.State = change.StateCancelled

	got, err := BuildJobResultEvidence(input)
	if err != nil {
		t.Fatalf("BuildJobResultEvidence() error = %v", err)
	}
	if got.OutputPresent || got.OutputDigest != "" || got.Outcome != job.StatusCancelled {
		t.Fatalf("unexpected cancellation evidence: %#v", got)
	}
}

func validJobResultEvidenceInput(now time.Time) JobResultEvidenceInput {
	created := now.Add(-2 * time.Minute)
	completed := now.Add(-30 * time.Second)
	return JobResultEvidenceInput{
		Change: change.Snapshot{
			ID: "chg-1", Action: "node.update", Requester: "operator", RevisionID: "rev-1",
			Risk: policy.RiskMedium, State: change.StateSucceeded,
			Decision: policy.Decision{Effect: policy.EffectAllow, Risk: policy.RiskMedium, Reason: "allowed", PolicyID: "policy-1"},
			Version:  6, UpdatedAt: completed,
		},
		Job: job.Job{
			ID: "job-1", ChangeID: "chg-1", ActionName: "node.update", Status: job.StatusSucceeded,
			Attempt: 1, MaxAttempts: 3, CreatedAt: created, UpdatedAt: completed, Version: 4,
			Output: &events.Output{
				ActualStates: []events.ActualState{{ResourceID: "node-1", Kind: "node", State: events.StatePresent, ObservedAt: completed.Add(-20 * time.Second), Details: json.RawMessage(`{"token":"super-secret"}`)}},
				Health: []events.Health{
					{ResourceID: "node-1", Status: events.HealthHealthy, CheckedAt: completed.Add(-15 * time.Second)},
					{ResourceID: "service-1", Status: events.HealthDegraded, CheckedAt: completed.Add(-10 * time.Second), Message: "backend-only-health-message"},
				},
				AuditEvents: []events.AuditEvent{{ID: "audit-1", OccurredAt: completed.Add(-5 * time.Second), Actor: "worker", Action: "node.update", ResourceID: "node-1", Outcome: "succeeded", CorrelationID: "corr-1", Details: json.RawMessage(`{"detail":"raw-audit-detail"}`)}},
			},
		},
		RevisionDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ObservedAt:     now,
	}
}

func containsResultEvidenceText(data []byte, text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i+len(text) <= len(data); i++ {
		if string(data[i:i+len(text)]) == text {
			return true
		}
	}
	return false
}
