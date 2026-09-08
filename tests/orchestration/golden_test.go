package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/config"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
	"control-center/internal/orchestration/worker"
)

type ensureServiceInput struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

var serviceSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["name","enabled"],"properties":{"name":{"type":"string"},"enabled":{"type":"boolean"}}}`)

type successReport struct {
	RevisionID     string        `json:"revisionId"`
	RevisionDigest string        `json:"revisionDigest"`
	ChangeState    change.State  `json:"changeState"`
	ChangeVersion  uint64        `json:"changeVersion"`
	JobStatus      job.Status    `json:"jobStatus"`
	Attempt        int           `json:"attempt"`
	Output         events.Output `json:"output"`
}

func TestGoldenE2E(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	executedAt := createdAt.Add(time.Second)
	revision, err := config.NewRevision("rev-0003", 3, createdAt, []byte(`{"service":"api","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := (policy.ThresholdEvaluator{PolicyID: "baseline-v1", ApprovalPermission: "changes.approve"}).Evaluate(
		policy.EvaluationInput{Action: "service.ensure", Requester: "operator", Risk: policy.RiskMedium},
	)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := change.New("change-0003", "service.ensure", "operator", revision, decision, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.Transition(change.StateQueued, 1, createdAt); err != nil {
		t.Fatal(err)
	}

	registry := action.NewRegistry()
	definition := action.NewTyped("service.ensure", "services.write", policy.RiskMedium, serviceSchema,
		func(_ context.Context, input ensureServiceInput) (events.Output, error) {
			return events.Output{
				ActualStates: []events.ActualState{{
					ResourceID: "service/" + input.Name, Kind: "service", State: events.StatePresent,
					ObservedAt: executedAt, Revision: revision.ID(), Details: json.RawMessage(`{"enabled":true}`),
				}},
				Health: []events.Health{{ResourceID: "service/" + input.Name, Status: events.HealthHealthy, CheckedAt: executedAt, Message: "service is active"}},
			}, nil
		},
		func(_ context.Context, input ensureServiceInput, output events.Output) error {
			if !input.Enabled || len(output.ActualStates) != 1 || output.ActualStates[0].State != events.StatePresent {
				return errors.New("service did not reach requested state")
			}
			return nil
		},
	)
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	repository := job.NewMemoryRepository()
	_, _, err = repository.Create(ctx, job.CreateRequest{
		ID: "job-0003", ChangeID: "change-0003", ActionName: "service.ensure",
		Input: json.RawMessage(`{"name":"api","enabled":true}`), IdempotencyKey: "request-0003", MaxAttempts: 3, Now: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.Transition(change.StateExecuting, 2, executedAt); err != nil {
		t.Fatal(err)
	}
	runner := worker.Worker{
		ID: "worker-1", Repository: repository, Registry: registry,
		Allowlist: worker.Set("service.ensure"), Permissions: worker.Set("services.write"),
		LeaseTTL: time.Minute, RetryPolicy: job.RetryPolicy{BaseDelay: time.Second}, Failures: worker.NoFailures{},
	}
	completed, claimed, err := runner.RunOne(ctx, executedAt)
	if err != nil || !claimed {
		t.Fatalf("run = %#v, claimed=%v, err=%v", completed, claimed, err)
	}
	if err := machine.Transition(change.StateVerifying, 3, executedAt); err != nil {
		t.Fatal(err)
	}
	if err := machine.Transition(change.StateSucceeded, 4, executedAt); err != nil {
		t.Fatal(err)
	}
	report := successReport{
		RevisionID: revision.ID(), RevisionDigest: revision.Digest(), ChangeState: machine.Snapshot().State,
		ChangeVersion: machine.Snapshot().Version, JobStatus: completed.Status, Attempt: completed.Attempt, Output: *completed.Output,
	}
	assertGolden(t, "golden_e2e.json", report)
}

type pointFailure struct{ point worker.FailurePoint }

func (f pointFailure) Inject(_ context.Context, point worker.FailurePoint, _ job.Job) error {
	if point == f.point {
		return errors.New("injected disk fault")
	}
	return nil
}

type failedRun struct {
	Status    job.Status    `json:"status"`
	Attempt   int           `json:"attempt"`
	LastError string        `json:"lastError"`
	Output    events.Output `json:"output"`
}

type failureReport struct {
	Verification failedRun `json:"verification"`
	Injected     failedRun `json:"injected"`
}

func TestGoldenFailure(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	verification := runFailure(t, "verify-job", now, worker.NoFailures{}, true)
	injected := runFailure(t, "inject-job", now, pointFailure{point: worker.FailureAfterExecute}, false)
	assertGolden(t, "golden_failure.json", failureReport{Verification: verification, Injected: injected})
}

func runFailure(t *testing.T, id string, now time.Time, injector worker.FailureInjector, failVerification bool) failedRun {
	t.Helper()
	ctx := context.Background()
	registry := action.NewRegistry()
	definition := action.NewTyped("service.ensure", "services.write", policy.RiskMedium, serviceSchema,
		func(_ context.Context, input ensureServiceInput) (events.Output, error) {
			state := events.StatePresent
			status := events.HealthHealthy
			message := "service is active"
			if failVerification {
				state, status, message = events.StateAbsent, events.HealthDegraded, "service is inactive"
			}
			return events.Output{
				ActualStates: []events.ActualState{{ResourceID: "service/" + input.Name, Kind: "service", State: state, ObservedAt: now}},
				Health:       []events.Health{{ResourceID: "service/" + input.Name, Status: status, CheckedAt: now, Message: message}},
			}, nil
		},
		func(_ context.Context, _ ensureServiceInput, output events.Output) error {
			if output.ActualStates[0].State != events.StatePresent {
				return errors.New("observed service is absent")
			}
			return nil
		},
	)
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	repository := job.NewMemoryRepository()
	_, _, err := repository.Create(ctx, job.CreateRequest{
		ID: id, ChangeID: "change-" + id, ActionName: "service.ensure", Input: json.RawMessage(`{"name":"api","enabled":true}`),
		IdempotencyKey: "key-" + id, MaxAttempts: 1, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := worker.Worker{
		ID: "worker-1", Repository: repository, Registry: registry,
		Allowlist: worker.Set("service.ensure"), Permissions: worker.Set("services.write"),
		LeaseTTL: time.Minute, RetryPolicy: job.RetryPolicy{BaseDelay: time.Second}, Failures: injector,
	}
	result, claimed, runErr := runner.RunOne(ctx, now)
	if !claimed || runErr == nil {
		t.Fatalf("expected claimed failed run, got claimed=%v err=%v", claimed, runErr)
	}
	return failedRun{Status: result.Status, Attempt: result.Attempt, LastError: result.LastError, Output: *result.Output}
}

func assertGolden(t *testing.T, name string, value any) {
	t.Helper()
	actual, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	expected, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("golden mismatch for %s\n--- actual ---\n%s\n--- expected ---\n%s", name, actual, expected)
	}
}
