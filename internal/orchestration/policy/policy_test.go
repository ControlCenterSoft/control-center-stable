package policy_test

import (
	"control-center/internal/orchestration/policy"
	"testing"
	"time"
)

func TestCriticalRiskRequiresTwoIndependentApprovers(t *testing.T) {
	evaluator := policy.ThresholdEvaluator{PolicyID: "baseline", ApprovalPermission: "changes.approve"}
	decision, err := evaluator.Evaluate(policy.EvaluationInput{Action: "network.route.replace", Requester: "alice", Risk: policy.RiskCritical})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1, 0)
	approvals := []policy.Approval{{Actor: "alice", Permissions: []string{"changes.approve"}, ApprovedAt: when}, {Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: when}, {Actor: "bob", Permissions: []string{"changes.approve"}, ApprovedAt: when}}
	if err := policy.CheckApprovals("alice", decision.Requirement, approvals); err == nil {
		t.Fatal("requester and duplicate approver must not satisfy two-person approval")
	}
	approvals = append(approvals, policy.Approval{Actor: "carol", Permissions: []string{"changes.approve"}, ApprovedAt: when})
	if err := policy.CheckApprovals("alice", decision.Requirement, approvals); err != nil {
		t.Fatalf("two independent approvers rejected: %v", err)
	}
}
