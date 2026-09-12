package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/identity/rbac"
	"control-center/internal/orchestration/change"
	orchestrationconfig "control-center/internal/orchestration/config"
	"control-center/internal/orchestration/policy"
)

func TestEvaluateJobAdmissionAcceptsExactCurrentApprovedRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	revision, record := approvedAdmissionRecord(t, now)
	server := &Server{
		revisions:       map[string]orchestrationconfig.Revision{revision.ID(): revision},
		currentRevision: revision.ID(),
		now:             func() time.Time { return now },
	}

	decision, err := server.evaluateJobAdmission(record)
	if err != nil {
		t.Fatalf("evaluate exact current admission: %v", err)
	}
	if !decision.Eligible {
		t.Fatalf("exact current approved Change must be eligible: %#v", decision)
	}
	if decision.ExecutionAuthorized {
		t.Fatal("preflight evidence must never authorize execution")
	}
	if decision.ChangeID != record.machine.Snapshot().ID || decision.RevisionID != revision.ID() || decision.RevisionDigest != revision.Digest() {
		t.Fatalf("decision is not bound to exact Change/revision: %#v", decision)
	}
}

func TestEvaluateJobAdmissionRejectsStaleApprovedRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	revision, record := approvedAdmissionRecord(t, now)
	newer, err := orchestrationconfig.NewRevision("rev-current", 2, now.Add(-10*time.Second), []byte(`{"generation":2}`))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		revisions: map[string]orchestrationconfig.Revision{
			revision.ID(): revision,
			newer.ID():    newer,
		},
		currentRevision: newer.ID(),
		now:             func() time.Time { return now },
	}

	decision, err := server.evaluateJobAdmission(record)
	if !errors.Is(err, errJobAdmissionBlocked) {
		t.Fatalf("stale approved Change must fail closed, err=%v decision=%#v", err, decision)
	}
	var blocked *jobAdmissionBlockError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected structured admission block error, got %T: %v", err, err)
	}
	if !containsAdmissionBlocker(blocked.Blockers, "revision_is_no_longer_current") {
		t.Fatalf("missing stale-revision blocker: %#v", blocked.Blockers)
	}
	if decision.Eligible || decision.ExecutionAuthorized {
		t.Fatalf("stale Change must not become eligible/authorized: %#v", decision)
	}
	if state := record.machine.Snapshot().State; state != change.StateApproved {
		t.Fatalf("preflight must be side-effect-free, state=%q", state)
	}
}

func TestEnqueueWithAdmissionPreflightStopsBeforeMutationForStaleRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	revision, record := approvedAdmissionRecord(t, now)
	newer, err := orchestrationconfig.NewRevision("rev-current", 2, now.Add(-10*time.Second), []byte(`{"generation":2}`))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		revisions: map[string]orchestrationconfig.Revision{
			revision.ID(): revision,
			newer.ID():    newer,
		},
		currentRevision: newer.ID(),
		now:             func() time.Time { return now },
	}

	err = server.enqueueWithAdmissionPreflight(context.Background(), record)
	if !errors.Is(err, errJobAdmissionBlocked) {
		t.Fatalf("stale Change must be stopped before durable enqueue, err=%v", err)
	}
	if state := record.machine.Snapshot().State; state != change.StateApproved {
		t.Fatalf("stale admission changed state before enqueue: %q", state)
	}
	if record.jobID != "" {
		t.Fatalf("stale admission created Job binding %q", record.jobID)
	}
}

func TestEvaluateJobAdmissionRejectsUnavailableImmutableRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	revision, record := approvedAdmissionRecord(t, now)
	server := &Server{
		revisions:       map[string]orchestrationconfig.Revision{},
		currentRevision: revision.ID(),
		now:             func() time.Time { return now },
	}

	decision, err := server.evaluateJobAdmission(record)
	if !errors.Is(err, errJobAdmissionBlocked) {
		t.Fatalf("missing immutable revision must fail closed, err=%v decision=%#v", err, decision)
	}
	if decision.Eligible || decision.ExecutionAuthorized {
		t.Fatalf("unavailable revision must not be eligible/authorized: %#v", decision)
	}
}

func TestEvaluateJobAdmissionRejectsUnavailableClockWithoutMutation(t *testing.T) {
	now := time.Date(2026, 9, 12, 5, 0, 0, 0, time.UTC)
	revision, record := approvedAdmissionRecord(t, now)
	server := &Server{
		revisions:       map[string]orchestrationconfig.Revision{revision.ID(): revision},
		currentRevision: revision.ID(),
	}

	decision, err := server.evaluateJobAdmission(record)
	if !errors.Is(err, errJobAdmissionBlocked) {
		t.Fatalf("missing admission clock must fail closed, err=%v decision=%#v", err, decision)
	}
	if decision.Eligible || decision.ExecutionAuthorized {
		t.Fatalf("missing admission clock must not become eligible/authorized: %#v", decision)
	}
	if state := record.machine.Snapshot().State; state != change.StateApproved {
		t.Fatalf("failed clock preflight mutated Change state: %q", state)
	}
	if record.jobID != "" {
		t.Fatalf("failed clock preflight created Job binding %q", record.jobID)
	}
}

func approvedAdmissionRecord(t *testing.T, now time.Time) (orchestrationconfig.Revision, *changeRecord) {
	t.Helper()
	revision, err := orchestrationconfig.NewRevision("rev-reviewed", 1, now.Add(-2*time.Minute), []byte(`{"generation":1}`))
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Decision{
		Effect:   policy.EffectAllow,
		Risk:     policy.RiskHigh,
		Reason:   "test policy allows reviewed high-risk action",
		PolicyID: "test-admission-v1",
		Requirement: policy.ApprovalRequirement{
			Minimum:           1,
			Permission:        string(rbac.PermissionChangesApprove),
			DistinctActors:    true,
			ProhibitRequester: true,
		},
	}
	machine, err := change.New("chg-reviewed", "test.high", "requester", revision, decision, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	approval := policy.Approval{
		Actor:       "approver",
		Permissions: []string{string(rbac.PermissionChangesApprove)},
		ApprovedAt:  now.Add(-30 * time.Second),
	}
	if err := machine.Approve(approval, machine.Snapshot().Version, approval.ApprovedAt); err != nil {
		t.Fatal(err)
	}
	if state := machine.Snapshot().State; state != change.StateApproved {
		t.Fatalf("fixture Change must be approved, state=%q", state)
	}
	return revision, &changeRecord{machine: machine, input: []byte(`{"name":"example"}`), idempotencyKey: "change-reviewed"}
}

func containsAdmissionBlocker(blockers []string, wanted string) bool {
	for _, blocker := range blockers {
		if blocker == wanted {
			return true
		}
	}
	return false
}
