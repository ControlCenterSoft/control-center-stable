package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildLifecyclePlanProviderOperationMatrix(t *testing.T) {
	tests := []struct {
		name          string
		provider      Provider
		operation     LifecycleOperation
		platform      string
		wantMutating  bool
		wantApply     LifecycleAction
		wantLast      LifecycleAction
		wantStepCount int
	}{
		{name: "samba create", provider: ProviderSamba, operation: LifecycleCreate, platform: "linux", wantMutating: true, wantApply: ActionSambaProvisionDomain, wantLast: ActionSambaCheckHealth, wantStepCount: 6},
		{name: "freeipa create", provider: ProviderFreeIPA, operation: LifecycleCreate, platform: "linux", wantMutating: true, wantApply: ActionFreeIPAProvisionDomain, wantLast: ActionFreeIPACheckHealth, wantStepCount: 6},
		{name: "samba join windows", provider: ProviderSamba, operation: LifecycleJoin, platform: "windows", wantMutating: true, wantApply: ActionSambaJoinMember, wantLast: ActionSambaVerifyMembership, wantStepCount: 6},
		{name: "freeipa join linux", provider: ProviderFreeIPA, operation: LifecycleJoin, platform: "linux", wantMutating: true, wantApply: ActionFreeIPAEnrollHost, wantLast: ActionFreeIPAVerifyMembership, wantStepCount: 6},
		{name: "samba promote", provider: ProviderSamba, operation: LifecyclePromote, platform: "linux", wantMutating: true, wantApply: ActionSambaPromoteController, wantLast: ActionSambaVerifyReplication, wantStepCount: 7},
		{name: "freeipa promote", provider: ProviderFreeIPA, operation: LifecyclePromote, platform: "linux", wantMutating: true, wantApply: ActionFreeIPAPromoteReplica, wantLast: ActionFreeIPAVerifyReplication, wantStepCount: 7},
		{name: "samba health preflight", provider: ProviderSamba, operation: LifecycleHealthPreflight, platform: "linux", wantMutating: false, wantApply: ActionSambaCheckHealth, wantLast: ActionSambaVerifyReplication, wantStepCount: 7},
		{name: "freeipa health preflight", provider: ProviderFreeIPA, operation: LifecycleHealthPreflight, platform: "linux", wantMutating: false, wantApply: ActionFreeIPACheckHealth, wantLast: ActionFreeIPAVerifyReplication, wantStepCount: 7},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := BuildLifecyclePlan(LifecyclePlanRequest{
				Provider:   test.provider,
				Operation:  test.operation,
				DomainName: "Example.TEST.",
				Target: LifecycleTarget{
					NodeID:   "node-01",
					Platform: test.platform,
				},
			})
			if err != nil {
				t.Fatalf("BuildLifecyclePlan() error = %v", err)
			}
			if plan.SchemaVersion != LifecyclePlanSchemaVersion || plan.DomainName != "example.test" {
				t.Fatalf("plan identity = %#v", plan)
			}
			if plan.Provider != test.provider || plan.Operation != test.operation || plan.Mutating != test.wantMutating {
				t.Fatalf("plan selection = %#v", plan)
			}
			if len(plan.Steps) != test.wantStepCount {
				t.Fatalf("steps = %#v", plan.Steps)
			}
			for index, step := range plan.Steps {
				if step.Order != index+1 {
					t.Fatalf("step %d order = %d", index, step.Order)
				}
			}
			if !containsLifecycleAction(plan.Steps, test.wantApply) {
				t.Fatalf("steps do not contain %q: %#v", test.wantApply, plan.Steps)
			}
			if plan.Steps[len(plan.Steps)-1].Action != test.wantLast {
				t.Fatalf("last action = %q, want %q", plan.Steps[len(plan.Steps)-1].Action, test.wantLast)
			}
			if !strings.HasPrefix(plan.PlanID, "sha256:") || len(plan.PlanID) != len("sha256:")+64 {
				t.Fatalf("plan id = %q", plan.PlanID)
			}
			if !test.wantMutating {
				for _, step := range plan.Steps {
					if step.Stage != LifecycleStagePreflight {
						t.Fatalf("read-only plan has %q stage: %#v", step.Stage, plan.Steps)
					}
				}
			}
		})
	}
}

func TestBuildLifecyclePlanIsCanonicalAndDeterministic(t *testing.T) {
	request := LifecyclePlanRequest{
		Provider:   legacyProviderSamba,
		Operation:  " PROMOTE ",
		DomainName: " EXAMPLE.Test. ",
		Target:     LifecycleTarget{NodeID: " dc-02 ", Platform: " LINUX "},
		Requirements: Requirements{
			WindowsDomainJoin: true,
			GroupPolicy:       true,
		},
	}
	first, err := BuildLifecyclePlan(request)
	if err != nil {
		t.Fatalf("first BuildLifecyclePlan() error = %v", err)
	}
	second, err := BuildLifecyclePlan(request)
	if err != nil {
		t.Fatalf("second BuildLifecyclePlan() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("plans differ:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.Provider != ProviderSamba || first.Operation != LifecyclePromote || first.DomainName != "example.test" {
		t.Fatalf("plan was not canonicalized: %#v", first)
	}
	if first.Target.NodeID != "dc-02" || first.Target.Platform != "linux" {
		t.Fatalf("target was not canonicalized: %#v", first.Target)
	}

	request.Target.NodeID = "dc-03"
	changed, err := BuildLifecyclePlan(request)
	if err != nil {
		t.Fatalf("changed BuildLifecyclePlan() error = %v", err)
	}
	if changed.PlanID == first.PlanID {
		t.Fatalf("different targets share plan id %q", first.PlanID)
	}
}

func TestBuildLifecyclePlanRejectsUnsafeOrIncompatibleInputs(t *testing.T) {
	valid := LifecyclePlanRequest{
		Provider:   ProviderSamba,
		Operation:  LifecycleCreate,
		DomainName: "example.test",
		Target:     LifecycleTarget{NodeID: "dc-01", Platform: "linux"},
	}
	tests := []struct {
		name   string
		mutate func(*LifecyclePlanRequest)
		want   error
	}{
		{name: "auto provider", mutate: func(request *LifecyclePlanRequest) { request.Provider = ProviderAuto }, want: ErrInvalidLifecyclePlan},
		{name: "unknown provider", mutate: func(request *LifecyclePlanRequest) { request.Provider = "custom" }, want: ErrInvalidLifecyclePlan},
		{name: "unknown operation", mutate: func(request *LifecyclePlanRequest) { request.Operation = "execute" }, want: ErrInvalidLifecyclePlan},
		{name: "single label domain", mutate: func(request *LifecyclePlanRequest) { request.DomainName = "example" }, want: ErrInvalidLifecyclePlan},
		{name: "ip address domain", mutate: func(request *LifecyclePlanRequest) { request.DomainName = "192.0.2.10" }, want: ErrInvalidLifecyclePlan},
		{name: "endpoint node id", mutate: func(request *LifecyclePlanRequest) { request.Target.NodeID = "https://node.test" }, want: ErrInvalidLifecyclePlan},
		{name: "windows create", mutate: func(request *LifecyclePlanRequest) { request.Target.Platform = "windows" }, want: ErrInvalidLifecyclePlan},
		{name: "freeipa windows join", mutate: func(request *LifecyclePlanRequest) {
			request.Provider = ProviderFreeIPA
			request.Operation = LifecycleJoin
			request.Target.Platform = "windows"
		}, want: ErrInvalidLifecyclePlan},
		{name: "freeipa windows requirement", mutate: func(request *LifecyclePlanRequest) {
			request.Provider = ProviderFreeIPA
			request.Requirements.WindowsDomainJoin = true
		}, want: ErrIncompatibleProvider},
		{name: "freeipa group policy requirement", mutate: func(request *LifecyclePlanRequest) {
			request.Provider = ProviderFreeIPA
			request.Requirements.GroupPolicy = true
		}, want: ErrIncompatibleProvider},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			_, err := BuildLifecyclePlan(request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func containsLifecycleAction(steps []LifecycleStep, want LifecycleAction) bool {
	for _, step := range steps {
		if step.Action == want {
			return true
		}
	}
	return false
}
