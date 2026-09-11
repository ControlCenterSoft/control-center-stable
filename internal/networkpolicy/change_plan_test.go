package networkpolicy

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validChangePlanRequest() ChangePlanRequest {
	return ChangePlanRequest{
		NodeID:     "node-01",
		RevisionID: "network-revision-7",
		Interfaces: []InterfaceIntent{
			{InterfaceID: "uplink-01", Zone: ZoneWAN, Changed: true},
			{InterfaceID: "service-01", Zone: ZoneLAN, Changed: true},
			{InterfaceID: "management-01", Zone: ZoneManagement},
		},
		Forwarding: []ForwardingIntent{
			{Source: ZoneWAN, Destination: ZoneLAN, ExplicitlyEnabled: true, EdgeGatewayAssigned: true},
		},
		Probes: []ConnectivityProbe{
			{ID: "wan-link", Kind: ProbeLinkState, InterfaceID: "uplink-01", Zone: ZoneWAN},
			{ID: "lan-reachability", Kind: ProbeZoneReachability, InterfaceID: "service-01", Zone: ZoneLAN},
			{ID: "management-control", Kind: ProbeControlPlane, InterfaceID: "management-01", Zone: ZoneManagement},
		},
		Timeouts: TimeoutPolicy{
			Snapshot: 30 * time.Second, Preflight: 45 * time.Second,
			ApplyWindow: 2 * time.Minute, Probe: 30 * time.Second, Rollback: time.Minute,
		},
	}
}

func TestBuildChangePlanCanonicalMultiNICSequence(t *testing.T) {
	request := validChangePlanRequest()
	request.NodeID = " node-01 "
	request.Interfaces[0].Zone = " wan "
	request.Probes[0].Zone = " wan "
	request.Forwarding[0].Source = " wan "
	request.Forwarding[0].Destination = " lan "

	plan, err := BuildChangePlan(request)
	if err != nil {
		t.Fatalf("BuildChangePlan() error = %v", err)
	}
	if plan.SchemaVersion != ChangePlanSchemaVersion || !strings.HasPrefix(plan.PlanID, "sha256:") || len(plan.PlanID) != len("sha256:")+64 {
		t.Fatalf("plan identity = %#v", plan)
	}
	if !plan.ForwardingDefaultDeny {
		t.Fatal("forwarding must remain default-deny")
	}
	if len(plan.Interfaces) != 3 || plan.Interfaces[0].InterfaceID != "management-01" {
		t.Fatalf("interfaces are not canonical: %#v", plan.Interfaces)
	}
	wantStages := []ChangeStage{
		ChangeStageSnapshot,
		ChangeStagePreflight,
		ChangeStagePreflight,
		ChangeStagePreflight,
		ChangeStageApplyWindow,
		ChangeStageProbes,
		ChangeStageDecision,
	}
	for index, step := range plan.Steps {
		if step.Order != index+1 || step.Stage != wantStages[index] {
			t.Fatalf("step %d = %#v", index, step)
		}
	}
}

func TestBuildChangePlanDeterministicAcrossInputOrder(t *testing.T) {
	firstRequest := validChangePlanRequest()
	secondRequest := validChangePlanRequest()
	secondRequest.Interfaces[0], secondRequest.Interfaces[2] = secondRequest.Interfaces[2], secondRequest.Interfaces[0]
	secondRequest.Probes[0], secondRequest.Probes[2] = secondRequest.Probes[2], secondRequest.Probes[0]

	first, err := BuildChangePlan(firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildChangePlan(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("canonical plans differ:\nfirst=%#v\nsecond=%#v", first, second)
	}

	secondRequest.RevisionID = "network-revision-8"
	changed, err := BuildChangePlan(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if changed.PlanID == first.PlanID {
		t.Fatalf("different revisions share plan id %q", first.PlanID)
	}
}

func TestBuildChangePlanFailsClosedOnUnsafeInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ChangePlanRequest)
		want   error
	}{
		{name: "no changed interface", mutate: func(request *ChangePlanRequest) {
			for index := range request.Interfaces {
				request.Interfaces[index].Changed = false
			}
		}, want: ErrInvalidChangePlan},
		{name: "changed interface without probe", mutate: func(request *ChangePlanRequest) {
			request.Probes = request.Probes[1:]
		}, want: ErrInvalidChangePlan},
		{name: "no management path probe", mutate: func(request *ChangePlanRequest) {
			request.Probes[2].Kind = ProbeLinkState
		}, want: ErrInvalidChangePlan},
		{name: "probe zone mismatch", mutate: func(request *ChangePlanRequest) {
			request.Probes[0].Zone = ZoneLAN
		}, want: ErrInvalidChangePlan},
		{name: "implicit WAN forwarding", mutate: func(request *ChangePlanRequest) {
			request.Forwarding[0].ExplicitlyEnabled = false
		}, want: ErrInvalidChangePlan},
		{name: "WAN without edge role", mutate: func(request *ChangePlanRequest) {
			request.Forwarding[0].EdgeGatewayAssigned = false
		}, want: ErrInvalidChangePlan},
		{name: "forwarding to undeclared zone", mutate: func(request *ChangePlanRequest) {
			request.Forwarding[0].Destination = ZoneDMZ
		}, want: ErrInvalidChangePlan},
		{name: "zero timeout", mutate: func(request *ChangePlanRequest) {
			request.Timeouts.Rollback = 0
		}, want: ErrInvalidChangePlan},
		{name: "unbounded apply window", mutate: func(request *ChangePlanRequest) {
			request.Timeouts.ApplyWindow = 16 * time.Minute
		}, want: ErrInvalidChangePlan},
		{name: "probe exceeds apply window", mutate: func(request *ChangePlanRequest) {
			request.Timeouts.Probe = 3 * time.Minute
		}, want: ErrInvalidChangePlan},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validChangePlanRequest()
			test.mutate(&request)
			_, err := BuildChangePlan(request)
			if !errors.Is(err, test.want) {
				t.Fatalf("BuildChangePlan() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeChangePlanRequestRejectsExecutionAndSecretFields(t *testing.T) {
	documents := []string{
		`{"node_id":"node-01","command":"ip link set dev eth0 down"}`,
		`{"node_id":"node-01","endpoint":"https://control.invalid"}`,
		`{"node_id":"node-01","credential":"not-a-real-secret"}`,
		`{"node_id":"node-01"} {"node_id":"node-02"}`,
	}
	for _, document := range documents {
		if _, err := DecodeChangePlanRequest([]byte(document)); !errors.Is(err, ErrInvalidChangePlan) {
			t.Fatalf("DecodeChangePlanRequest(%q) error = %v", document, err)
		}
	}
}
