package policy_test

import (
	"testing"
	"time"

	"control-center/internal/orchestration/policy"
)

func TestCheckApprovalsRejectsWhitespaceRequesterBypass(t *testing.T) {
	requirement := policy.ApprovalRequirement{
		Minimum: 1, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	}
	approvals := []policy.Approval{{
		Actor: "alice", Permissions: []string{"changes.approve"}, ApprovedAt: time.Unix(1, 0).UTC(),
	}}
	if err := policy.CheckApprovals(" alice ", requirement, approvals); err == nil {
		t.Fatal("non-canonical requester must not bypass requester-prohibited approval policy")
	}
}

func TestCheckApprovalsDoesNotCountWhitespaceActorAsDistinctApprover(t *testing.T) {
	requirement := policy.ApprovalRequirement{
		Minimum: 1, Permission: "changes.approve", DistinctActors: true, ProhibitRequester: true,
	}
	approvals := []policy.Approval{{
		Actor: " alice ", Permissions: []string{"changes.approve"}, ApprovedAt: time.Unix(1, 0).UTC(),
	}}
	if err := policy.CheckApprovals("alice", requirement, approvals); err == nil {
		t.Fatal("non-canonical actor must not satisfy approval policy")
	}
}

func TestApprovalRequirementRejectsNonCanonicalPermission(t *testing.T) {
	requirement := policy.ApprovalRequirement{Minimum: 1, Permission: " changes.approve "}
	if err := requirement.Validate(); err == nil {
		t.Fatal("non-canonical permission must be rejected")
	}
}
