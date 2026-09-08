package events

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validOutput(now time.Time) Output {
	return Output{
		ActualStates: []ActualState{{
			ResourceID: "service/api", Kind: "service", State: StatePresent,
			ObservedAt: now, Revision: "rev-1", Details: json.RawMessage(`{"enabled":true}`),
		}},
		Health: []Health{{
			ResourceID: "service/api", Status: HealthHealthy, CheckedAt: now,
			Message: "service is active",
		}},
		AuditEvents: []AuditEvent{{
			ID: "audit-1", OccurredAt: now, Actor: "worker-1", Action: "service.ensure",
			ResourceID: "service/api", Outcome: "succeeded", CorrelationID: "change-1",
			Details: json.RawMessage(`{"attempt":1}`),
		}},
	}
}

func TestSuccessfulOutputValidationAndCanonicalization(t *testing.T) {
	zone := time.FixedZone("test", 3*60*60)
	input := validOutput(time.Date(2026, 9, 8, 12, 0, 0, 0, zone))
	if err := input.ValidateSuccessful(); err != nil {
		t.Fatal(err)
	}
	canonical := input.Canonical()
	if canonical.ActualStates == nil || canonical.Health == nil || canonical.AuditEvents == nil {
		t.Fatal("canonical output must use non-nil arrays")
	}
	if canonical.ActualStates[0].ObservedAt.Location() != time.UTC ||
		canonical.Health[0].CheckedAt.Location() != time.UTC ||
		canonical.AuditEvents[0].OccurredAt.Location() != time.UTC {
		t.Fatal("canonical output timestamps must be UTC")
	}
	input.ActualStates[0].Details[0] = '['
	if string(canonical.ActualStates[0].Details) != `{"enabled":true}` {
		t.Fatal("canonical output retained caller-owned details bytes")
	}

	empty := (Output{}).Canonical()
	payload, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"actualStates":[],"health":[],"auditEvents":[]}` {
		t.Fatalf("empty output is not canonical JSON: %s", payload)
	}
}

func TestSuccessfulOutputRequiresOneToOneStateAndHealth(t *testing.T) {
	now := time.Now().UTC()
	stateOnly := validOutput(now)
	stateOnly.Health = nil
	if err := stateOnly.Validate(); err != nil {
		t.Fatalf("partial failure output should remain structurally valid: %v", err)
	}
	if err := stateOnly.ValidateSuccessful(); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("successful state without health accepted: %v", err)
	}

	healthOnly := validOutput(now)
	healthOnly.ActualStates = nil
	if err := healthOnly.ValidateSuccessful(); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("successful health without state accepted: %v", err)
	}
}

func TestOutputValidationRejectsAmbiguousAndUnboundedEvidence(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*Output)
	}{
		{name: "duplicate actual state", mutate: func(output *Output) {
			output.ActualStates = append(output.ActualStates, output.ActualStates[0])
		}},
		{name: "unknown resource state", mutate: func(output *Output) {
			output.ActualStates[0].State = ResourceState("invented")
		}},
		{name: "unknown health", mutate: func(output *Output) {
			output.Health[0].Status = HealthStatus("invented")
		}},
		{name: "missing timestamp", mutate: func(output *Output) {
			output.Health[0].CheckedAt = time.Time{}
		}},
		{name: "control character", mutate: func(output *Output) {
			output.AuditEvents[0].Actor = "worker\nforged"
		}},
		{name: "invalid details", mutate: func(output *Output) {
			output.ActualStates[0].Details = json.RawMessage(`[]`)
		}},
		{name: "oversized details", mutate: func(output *Output) {
			output.AuditEvents[0].Details = json.RawMessage(`{"value":"` + strings.Repeat("x", maxDetailsSize) + `"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := validOutput(now)
			test.mutate(&output)
			if err := output.Validate(); !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("invalid output accepted: %v", err)
			}
		})
	}
}
