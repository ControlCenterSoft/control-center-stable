package executionguard

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/config"
	"control-center/internal/orchestration/policy"
)

func TestEvaluateEligibleExactCurrentRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	snapshot := testSnapshot(revision, now.Add(-5*time.Minute), policy.RiskLow, policy.ApprovalRequirement{})

	decision, err := Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: revision.ID(),
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Eligible || decision.ExecutionAuthorized {
		t.Fatalf("unexpected preflight decision: %#v", decision)
	}
	if len(decision.Blockers) != 0 {
		t.Fatalf("eligible decision has blockers: %#v", decision.Blockers)
	}
	if decision.RevisionDigest != revision.Digest() || decision.RevisionID != revision.ID() {
		t.Fatalf("decision lost exact revision binding: %#v", decision)
	}
}

func TestEvaluateBlocksStaleApprovedRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	snapshot := testSnapshot(revision, now.Add(-5*time.Minute), policy.RiskLow, policy.ApprovalRequirement{})

	decision, err := Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: "revision-031-b",
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Eligible || decision.ExecutionAuthorized {
		t.Fatalf("stale approved change became executable: %#v", decision)
	}
	assertBlocker(t, decision, "revision_is_no_longer_current")
}

func TestEvaluateBlocksRevisionBindingMismatch(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	snapshot := testSnapshot(revision, now.Add(-5*time.Minute), policy.RiskLow, policy.ApprovalRequirement{})
	snapshot.RevisionID = "revision-031-other"

	decision, err := Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: snapshot.RevisionID,
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBlocker(t, decision, "revision_binding_mismatch")
	if decision.Eligible || decision.ExecutionAuthorized {
		t.Fatalf("revision mismatch became executable: %#v", decision)
	}
}

func TestEvaluateRevalidatesEffectiveApprovals(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	requirement := policy.ApprovalRequirement{
		Minimum:           1,
		Permission:        "changes.approve",
		DistinctActors:    true,
		ProhibitRequester: true,
	}
	snapshot := testSnapshot(revision, now.Add(-5*time.Minute), policy.RiskHigh, requirement)

	decision, err := Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: revision.ID(),
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBlocker(t, decision, "approval_requirement_not_satisfied")

	snapshot.Approvals = []policy.Approval{{
		Actor:       "reviewer-a",
		Permissions: []string{"changes.approve"},
		ApprovedAt:  now.Add(-time.Minute),
	}}
	decision, err = Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: revision.ID(),
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Eligible {
		t.Fatalf("valid independent approval did not satisfy preflight: %#v", decision)
	}
}

func TestEvaluateMaintenanceWindowFailClosed(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	snapshot := testSnapshot(revision, now.Add(-5*time.Minute), policy.RiskLow, policy.ApprovalRequirement{})

	tests := []struct {
		name    string
		window  *MaintenanceWindow
		blocker string
	}{
		{name: "missing", blocker: "maintenance_window_required"},
		{
			name: "not started",
			window: &MaintenanceWindow{
				StartsAt: now.Add(time.Minute),
				EndsAt:   now.Add(time.Hour),
			},
			blocker: "maintenance_window_not_started",
		},
		{
			name: "closed",
			window: &MaintenanceWindow{
				StartsAt: now.Add(-time.Hour),
				EndsAt:   now,
			},
			blocker: "maintenance_window_closed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := Evaluate(Input{
				Change:                   snapshot,
				Revision:                 revision,
				CurrentRevisionID:        revision.ID(),
				RequireMaintenanceWindow: true,
				MaintenanceWindow:        test.window,
				Now:                      now,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertBlocker(t, decision, test.blocker)
			if decision.Eligible || decision.ExecutionAuthorized {
				t.Fatalf("invalid maintenance window became executable: %#v", decision)
			}
		})
	}
}

func TestEvaluateRejectsCorruptPolicyEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	snapshot := testSnapshot(revision, now.Add(-5*time.Minute), policy.RiskLow, policy.ApprovalRequirement{})
	snapshot.Decision.Risk = policy.RiskHigh

	if _, err := Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: revision.ID(),
		Now:               now,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected corrupt policy evidence to fail closed, got %v", err)
	}
}

func TestEvaluateRejectsFutureChangeEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 20, 0, 0, time.UTC)
	revision := testRevision(t, "revision-031-a", now.Add(-time.Hour))
	snapshot := testSnapshot(revision, now.Add(time.Minute), policy.RiskLow, policy.ApprovalRequirement{})

	if _, err := Evaluate(Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: revision.ID(),
		Now:               now,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("future-dated change evidence must fail closed, got %v", err)
	}
}

func testRevision(t *testing.T, id string, createdAt time.Time) config.Revision {
	t.Helper()
	revision, err := config.NewRevision(id, 1, createdAt, []byte(`{"service":"api","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func testSnapshot(revision config.Revision, updatedAt time.Time, risk policy.Risk, requirement policy.ApprovalRequirement) change.Snapshot {
	return change.Snapshot{
		ID:         "change-031-a",
		Action:     "service.ensure",
		Requester:  "operator-a",
		RevisionID: revision.ID(),
		Risk:       risk,
		State:      change.StateApproved,
		Decision: policy.Decision{
			Effect:      policy.EffectAllow,
			Risk:        risk,
			Reason:      "qualified test policy",
			Requirement: requirement,
			PolicyID:    "baseline",
		},
		Version:   3,
		UpdatedAt: updatedAt,
	}
}

func assertBlocker(t *testing.T, decision Decision, wanted string) {
	t.Helper()
	for _, blocker := range decision.Blockers {
		if blocker == wanted {
			return
		}
	}
	t.Fatalf("missing blocker %q in %#v", wanted, decision.Blockers)
}
