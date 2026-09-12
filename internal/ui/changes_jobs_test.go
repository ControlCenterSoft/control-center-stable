package ui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

func TestBuildChangesJobsViewStaysUnavailableUntilBothSourcesLoaded(t *testing.T) {
	view, err := BuildChangesJobsView(ChangesJobsInput{
		ChangesLoaded: true,
		Now:           time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != ChangesJobsUnavailable || view.GeneratedAt != nil || view.ChangeCount != 0 || view.JobCount != 0 || len(view.Changes) != 0 {
		t.Fatalf("unloaded job source must not look like confirmed empty state: %#v", view)
	}
}

func TestBuildChangesJobsViewProjectsOperationalStateWithoutSecrets(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	changeSnapshot := validChangeSnapshot("change-a", "service.ensure", now.Add(-10*time.Minute))
	output := &events.Output{
		ActualStates: []events.ActualState{{ResourceID: "resource-a", Kind: "service", State: events.StatePresent, ObservedAt: now.Add(-time.Minute), Details: json.RawMessage(`{"secret":"actual-state-secret"}`)}},
		Health: []events.Health{
			{ResourceID: "resource-a", Status: events.HealthHealthy, CheckedAt: now.Add(-2 * time.Minute), Message: "health-secret"},
			{ResourceID: "resource-b", Status: events.HealthFailed, CheckedAt: now.Add(-time.Minute), Message: "failure-secret"},
		},
		AuditEvents: []events.AuditEvent{{ID: "audit-a", OccurredAt: now, Actor: "system", Action: "execute", Outcome: "failed", CorrelationID: "corr-a", Details: json.RawMessage(`{"token":"audit-secret"}`)}},
	}
	jobs := []job.Job{{
		ID:             "job-a",
		ChangeID:       "change-a",
		ActionName:     "service.ensure",
		Input:          json.RawMessage(`{"password":"input-secret"}`),
		IdempotencyKey: "idempotency-secret",
		Status:         job.StatusRunning,
		Attempt:        1,
		MaxAttempts:    3,
		Lease:          &job.Lease{Token: "lease-token-secret", WorkerID: "internal-worker-secret", ExpiresAt: now.Add(5 * time.Minute)},
		Output:         output,
		LastError:      "database password=last-error-secret",
		CreatedAt:      now.Add(-5 * time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
		Version:        2,
	}}

	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{changeSnapshot},
		Jobs:          jobs,
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != ChangesJobsCurrent || view.ChangeCount != 1 || view.JobCount != 1 {
		t.Fatalf("unexpected view: %#v", view)
	}
	projected := view.Changes[0].Jobs[0]
	if !projected.LeaseHeld || !projected.FailureRecorded || !projected.Output.EvidenceAvailable {
		t.Fatalf("safe execution summary missing: %#v", projected)
	}
	if projected.Output.WorstHealth != events.HealthFailed || projected.Output.ActualStates != 1 || projected.Output.HealthChecks != 2 || projected.Output.AuditEvents != 1 {
		t.Fatalf("output summary=%#v", projected.Output)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		"input-secret", "idempotency-secret", "lease-token-secret", "internal-worker-secret", "last-error-secret",
		"actual-state-secret", "health-secret", "failure-secret", "audit-secret",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("read model leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestBuildChangesJobsViewSortsNewestChangeAndJobFirst(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	older := validChangeSnapshot("change-old", "service.ensure", now.Add(-20*time.Minute))
	newer := validChangeSnapshot("change-new", "service.ensure", now.Add(-10*time.Minute))
	jobs := []job.Job{
		validJob("job-old", "change-new", "service.ensure", now.Add(-9*time.Minute)),
		validJob("job-new", "change-new", "service.ensure", now.Add(-5*time.Minute)),
	}
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes: []change.Snapshot{older, newer}, Jobs: jobs,
		ChangesLoaded: true, JobsLoaded: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Changes[0].ID != "change-new" || view.Changes[0].Jobs[0].ID != "job-new" {
		t.Fatalf("unexpected ordering: %#v", view.Changes)
	}
}

func TestBuildChangesJobsViewRejectsOrphanJobWithoutPartialView(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now)},
		Jobs:          []job.Job{validJob("job-a", "missing-change", "service.ensure", now)},
		ChangesLoaded: true, JobsLoaded: true, Now: now,
	})
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("err=%v", err)
	}
	if view.ContractVersion != "" || view.State != "" || len(view.Changes) != 0 {
		t.Fatalf("invalid sources must not produce a partial view: %#v", view)
	}
}

func TestBuildChangesJobsViewRejectsActionMismatch(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	_, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now)},
		Jobs:          []job.Job{validJob("job-a", "change-a", "network.apply", now)},
		ChangesLoaded: true, JobsLoaded: true, Now: now,
	})
	if !errors.Is(err, ErrInvalidChangesJobsView) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildChangesJobsViewKeepsUnwiredRiskEvidenceUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	view, err := BuildChangesJobsView(ChangesJobsInput{
		Changes:       []change.Snapshot{validChangeSnapshot("change-a", "service.ensure", now)},
		ChangesLoaded: true, JobsLoaded: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	changeView := view.Changes[0]
	if changeView.SemanticDiff != EvidenceUnavailable || changeView.BlastRadius != EvidenceUnavailable || changeView.MaintenanceWindow != EvidenceUnavailable || changeView.RecoveryEvidence != EvidenceUnavailable {
		t.Fatalf("unwired evidence was invented: %#v", changeView)
	}
}

func validChangeSnapshot(id, action string, updatedAt time.Time) change.Snapshot {
	decision := policy.Decision{
		Effect:   policy.EffectAllow,
		Risk:     policy.RiskLow,
		Reason:   "test policy allows low risk operation",
		PolicyID: "policy-test",
	}
	return change.Snapshot{
		ID: id, Action: action, Requester: "operator-a", RevisionID: "revision-a",
		Risk: policy.RiskLow, State: change.StateApproved, Decision: decision,
		Version: 1, UpdatedAt: updatedAt,
	}
}

func validJob(id, changeID, action string, updatedAt time.Time) job.Job {
	return job.Job{
		ID: id, ChangeID: changeID, ActionName: action, Status: job.StatusQueued,
		Attempt: 0, MaxAttempts: 3, CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt, Version: 1,
	}
}
