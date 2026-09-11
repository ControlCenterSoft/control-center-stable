package siteoffline

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)

func testDigest(value byte) string {
	return strings.Repeat(string([]byte{value}), 64)
}

func validPolicy() PolicySnapshot {
	return PolicySnapshot{
		PolicyID:        "offline-policy-site-a",
		SiteID:          "site-a",
		SiteScopeID:     "scope-site-a",
		Generation:      7,
		ResourceVersion: "rv:policy:7",
		ValidFrom:       testNow.Add(-time.Hour),
		ValidUntil:      testNow.Add(time.Hour),
		Delegations: []Delegation{
			{ScopeID: "scope-site-a", Class: OperationUI, Action: "ui.dashboard.view"},
			{ScopeID: "scope-site-a", Class: OperationJob, Action: "job.restart.plan"},
			{ScopeID: "scope-site-a", Class: OperationMarket, Action: "market.install.plan"},
		},
	}
}

func validRequest(class OperationClass, action string) AdmissionRequest {
	return AdmissionRequest{
		RequestID:                     "request-1",
		ActorID:                       "operator-1",
		SiteID:                        "site-a",
		TargetScopeID:                 "scope-site-a",
		Connectivity:                  WANUnavailable,
		Class:                         class,
		Action:                        action,
		ObjectID:                      "service-1",
		IntentDigest:                  testDigest('a'),
		BaseGeneration:                12,
		BaseResourceVersion:           "rv:service:12",
		QueueSequence:                 41,
		RequestedAt:                   testNow,
		ExpectedPolicyGeneration:      7,
		ExpectedPolicyResourceVersion: "rv:policy:7",
	}
}

func TestBuildAdmissionDecisionAllowsOnlyExactDelegations(t *testing.T) {
	tests := []struct {
		class  OperationClass
		action string
	}{
		{OperationUI, "ui.dashboard.view"},
		{OperationJob, "job.restart.plan"},
		{OperationMarket, "market.install.plan"},
	}
	for _, test := range tests {
		t.Run(string(test.class), func(t *testing.T) {
			request := validRequest(test.class, test.action)
			got, err := BuildAdmissionDecision(request, validPolicy())
			if err != nil {
				t.Fatalf("BuildAdmissionDecision() error = %v", err)
			}
			if got.Effect != EffectAllow || got.Reason != ReasonAllowed || got.QueueEntry == nil {
				t.Fatalf("decision = %#v, want queued allow", got)
			}
			if got.ProductionMutationEnabled || !got.RequiresAudit || !got.RequiresReconnectReview {
				t.Fatalf("unsafe decision flags: %#v", got)
			}
			entry := got.QueueEntry
			if entry.ContractVersion != QueueEntryContractV1 || entry.ConflictMode != "reject_on_divergence" ||
				entry.ConflictKey != "scope-site-a/service-1" || entry.IntentDigest != request.IntentDigest {
				t.Fatalf("queue entry = %#v", entry)
			}
		})
	}
}

func TestBuildAdmissionDecisionIsDeterministicAndCanonicalizesDelegationOrder(t *testing.T) {
	request := validRequest(OperationJob, "job.restart.plan")
	policy := validPolicy()
	first, err := BuildAdmissionDecision(request, policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Delegations[0], policy.Delegations[2] = policy.Delegations[2], policy.Delegations[0]
	second, err := BuildAdmissionDecision(request, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.DecisionID != second.DecisionID || first.QueueEntry.QueueID != second.QueueEntry.QueueID {
		t.Fatalf("identical intent produced different IDs: %#v %#v", first, second)
	}
}

func TestBuildAdmissionDecisionDeniesAuthorityAndFreshnessFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdmissionRequest, *PolicySnapshot)
		want   ReasonCode
	}{
		{name: "wan is available", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.Connectivity = WANAvailable }, want: ReasonWANAvailable},
		{name: "different site", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.SiteID = "site-b" }, want: ReasonSiteMismatch},
		{name: "cross scope", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.TargetScopeID = "scope-site-b" }, want: ReasonCrossScope},
		{name: "not yet valid", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.RequestedAt = testNow.Add(-2 * time.Hour) }, want: ReasonPolicyNotYetValid},
		{name: "expired", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.RequestedAt = testNow.Add(time.Hour) }, want: ReasonPolicyExpired},
		{name: "stale generation", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.ExpectedPolicyGeneration = 6 }, want: ReasonPolicyGenerationMismatch},
		{name: "unexpected newer generation", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.ExpectedPolicyGeneration = 8 }, want: ReasonPolicyGenerationMismatch},
		{name: "resource version mismatch", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.ExpectedPolicyResourceVersion = "rv:policy:old" }, want: ReasonPolicyResourceVersionMismatch},
		{name: "non delegated", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.Action = "job.remove.plan" }, want: ReasonOperationNotDelegated},
		{name: "wrong class", mutate: func(r *AdmissionRequest, _ *PolicySnapshot) { r.Class = OperationMarket }, want: ReasonOperationNotDelegated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest(OperationJob, "job.restart.plan")
			policy := validPolicy()
			test.mutate(&request, &policy)
			got, err := BuildAdmissionDecision(request, policy)
			if err != nil {
				t.Fatalf("BuildAdmissionDecision() error = %v", err)
			}
			if got.Effect != EffectDeny || got.Reason != test.want || got.QueueEntry != nil || got.ProductionMutationEnabled {
				t.Fatalf("decision = %#v, want deny %q", got, test.want)
			}
		})
	}
}

func TestBuildAdmissionDecisionRejectsMalformedInputAndUnsafePolicy(t *testing.T) {
	request := validRequest(OperationJob, "job.restart.plan")
	request.IntentDigest = "raw operation payload"
	if _, err := BuildAdmissionDecision(request, validPolicy()); !errors.Is(err, ErrInvalidAdmissionRequest) {
		t.Fatalf("invalid digest error = %v", err)
	}

	request = validRequest(OperationJob, "job.restart.plan")
	policy := validPolicy()
	policy.Delegations = append(policy.Delegations, Delegation{
		ScopeID: "scope-other", Class: OperationJob, Action: "job.restart.plan",
	})
	if _, err := BuildAdmissionDecision(request, policy); !errors.Is(err, ErrInvalidOfflinePolicy) {
		t.Fatalf("cross-scope policy error = %v", err)
	}
}

func TestAdmissionJSONContainsMetadataOnly(t *testing.T) {
	decision, err := BuildAdmissionDecision(validRequest(OperationMarket, "market.install.plan"), validPolicy())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"payload", "credential", "password", "token", "command", "endpoint"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("decision contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}
