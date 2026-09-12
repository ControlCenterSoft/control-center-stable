package operationsview

import (
	"errors"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/policy"
)

func TestBuildApprovalEvidenceSatisfiedAndBoundToRevision(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 40, 0, 0, time.UTC)
	snapshot := approvalSnapshot(now, policy.ApprovalRequirement{
		Minimum: 1, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	})
	snapshot.State = change.StateApproved
	snapshot.Approvals = []policy.Approval{{
		Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-time.Minute),
	}}

	evidence, err := BuildApprovalEvidence(ApprovalEvidenceInput{
		Change: snapshot, RevisionDigest: testApprovalDigest("a"), ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != ApprovalEvidenceSatisfied || !evidence.Satisfied {
		t.Fatalf("approval evidence was not satisfied: %#v", evidence)
	}
	if evidence.ChangeID != snapshot.ID || evidence.RevisionID != snapshot.RevisionID || evidence.RevisionDigest != testApprovalDigest("a") {
		t.Fatalf("approval evidence lost exact revision binding: %#v", evidence)
	}
	if evidence.RecordedCount != 1 || evidence.EligibleCount != 1 || len(evidence.EligibleApprovals) != 1 {
		t.Fatalf("unexpected approval counts: %#v", evidence)
	}
	if len(evidence.EligibleApprovals) != 1 || evidence.EligibleApprovals[0].Actor != "bob" {
		t.Fatalf("eligible approval actor missing: %#v", evidence.EligibleApprovals)
	}
}

func TestBuildApprovalEvidenceMatchesRequesterAndDuplicatePolicy(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 41, 0, 0, time.UTC)
	snapshot := approvalSnapshot(now, policy.ApprovalRequirement{
		Minimum: 2, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	})
	snapshot.Risk = policy.RiskCritical
	snapshot.Decision.Risk = policy.RiskCritical
	snapshot.State = change.StateApproved
	snapshot.Approvals = []policy.Approval{
		{Actor: "alice", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-5 * time.Minute)},
		{Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-4 * time.Minute)},
		{Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-3 * time.Minute)},
		{Actor: "carol", Permissions: []string{"changes.read"}, ApprovedAt: now.Add(-2 * time.Minute)},
		{Actor: "dave", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-time.Minute)},
	}

	evidence, err := BuildApprovalEvidence(ApprovalEvidenceInput{
		Change: snapshot, RevisionDigest: testApprovalDigest("b"), ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.RecordedCount != 5 || evidence.EligibleCount != 2 || !evidence.Satisfied {
		t.Fatalf("policy eligibility was projected incorrectly: %#v", evidence)
	}
	if got := []string{evidence.EligibleApprovals[0].Actor, evidence.EligibleApprovals[1].Actor}; got[0] != "bob" || got[1] != "dave" {
		t.Fatalf("unexpected eligible actors: %v", got)
	}
}

func TestBuildApprovalEvidencePendingIsNotOptimistic(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 42, 0, 0, time.UTC)
	snapshot := approvalSnapshot(now, policy.ApprovalRequirement{
		Minimum: 2, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	})
	snapshot.Risk = policy.RiskCritical
	snapshot.Decision.Risk = policy.RiskCritical
	snapshot.Approvals = []policy.Approval{{
		Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-time.Minute),
	}}

	evidence, err := BuildApprovalEvidence(ApprovalEvidenceInput{
		Change: snapshot, RevisionDigest: testApprovalDigest("c"), ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != ApprovalEvidencePending || evidence.Satisfied || evidence.EligibleCount != 1 {
		t.Fatalf("insufficient approvals were shown optimistically: %#v", evidence)
	}
}

func TestBuildApprovalEvidenceRejectsProgressedChangeWithoutApprovals(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 43, 0, 0, time.UTC)
	snapshot := approvalSnapshot(now, policy.ApprovalRequirement{
		Minimum: 1, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	})
	snapshot.State = change.StateQueued

	_, err := BuildApprovalEvidence(ApprovalEvidenceInput{
		Change: snapshot, RevisionDigest: testApprovalDigest("d"), ObservedAt: now,
	})
	if !errors.Is(err, ErrInvalidApprovalEvidence) {
		t.Fatalf("progressed Change without approval evidence must fail closed, got %v", err)
	}
}

func TestBuildApprovalEvidenceRejectsMalformedOrFutureEvidence(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 44, 0, 0, time.UTC)
	snapshot := approvalSnapshot(now, policy.ApprovalRequirement{
		Minimum: 1, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	})
	snapshot.Approvals = []policy.Approval{{
		Actor: " bob ", Permissions: []string{"changes.approve"}, ApprovedAt: now.Add(-time.Minute),
	}}
	if _, err := BuildApprovalEvidence(ApprovalEvidenceInput{Change: snapshot, RevisionDigest: testApprovalDigest("e"), ObservedAt: now}); !errors.Is(err, ErrInvalidApprovalEvidence) {
		t.Fatalf("non-canonical actor must fail closed, got %v", err)
	}

	snapshot.Approvals[0].Actor = "bob"
	snapshot.Approvals[0].ApprovedAt = now.Add(time.Minute)
	if _, err := BuildApprovalEvidence(ApprovalEvidenceInput{Change: snapshot, RevisionDigest: testApprovalDigest("e"), ObservedAt: now}); !errors.Is(err, ErrInvalidApprovalEvidence) {
		t.Fatalf("future-dated approval must fail closed, got %v", err)
	}

	snapshot.Approvals[0].ApprovedAt = now.Add(-time.Minute)
	if _, err := BuildApprovalEvidence(ApprovalEvidenceInput{Change: snapshot, RevisionDigest: "sha256:ABC", ObservedAt: now}); !errors.Is(err, ErrInvalidApprovalEvidence) {
		t.Fatalf("invalid revision digest must fail closed, got %v", err)
	}
}

func TestBuildApprovalEvidenceDeniedDoesNotBecomeSatisfied(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 45, 0, 0, time.UTC)
	snapshot := approvalSnapshot(now, policy.ApprovalRequirement{})
	snapshot.Decision.Effect = policy.EffectDeny
	snapshot.Decision.Reason = "action denied"
	snapshot.State = change.StateRejected

	evidence, err := BuildApprovalEvidence(ApprovalEvidenceInput{
		Change: snapshot, RevisionDigest: testApprovalDigest("f"), ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != ApprovalEvidenceDenied || evidence.Satisfied {
		t.Fatalf("denied policy was represented as satisfied: %#v", evidence)
	}
}

func approvalSnapshot(now time.Time, requirement policy.ApprovalRequirement) change.Snapshot {
	return change.Snapshot{
		ID:         "change-a",
		Action:     "service.ensure",
		Requester:  "alice",
		RevisionID: "revision-a",
		Risk:       policy.RiskHigh,
		State:      change.StatePendingApproval,
		Decision: policy.Decision{
			Effect: policy.EffectAllow, Risk: policy.RiskHigh, Reason: "risk policy", PolicyID: "policy-a", Requirement: requirement,
		},
		Version:   1,
		UpdatedAt: now.Add(-10 * time.Minute),
	}
}

func testApprovalDigest(hexDigit string) string {
	return "sha256:" + strings.Repeat(hexDigit, 64)
}
