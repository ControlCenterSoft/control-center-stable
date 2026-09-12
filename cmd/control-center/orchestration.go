package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"control-center/internal/corecontracts"
	coreactionapi "control-center/internal/corecontracts/actionapi"
	identityapi "control-center/internal/identity/httpapi"
	"control-center/internal/identity/rbac"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/events"
	orchestrationapi "control-center/internal/orchestration/httpapi"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
	"control-center/internal/orchestration/worker"
	"control-center/internal/persistence/postgres"
)

type recordResourceInput struct {
	ResourceID string               `json:"resourceId"`
	Kind       string               `json:"kind"`
	State      events.ResourceState `json:"state"`
}

func newOrchestrationHandler(identity *identityapi.Server, db *sql.DB, middleware func(http.Handler) http.Handler, coreObjects corecontracts.ObjectRepository) (*orchestrationapi.Server, worker.Worker, error) {
	registry := action.NewRegistry()
	definition := action.NewTyped(
		"resource.record",
		string(rbac.PermissionActionsExecute),
		policy.RiskMedium,
		json.RawMessage(`{"type":"object","additionalProperties":false,"required":["resourceId","kind","state"]}`),
		func(ctx context.Context, input recordResourceInput) (events.Output, error) {
			if input.ResourceID == "" || input.Kind == "" {
				return events.Output{}, errors.New("resourceId and kind are required")
			}
			switch input.State {
			case events.StatePresent, events.StateAbsent, events.StateUnknown:
			default:
				return events.Output{}, errors.New("invalid resource state")
			}
			invocation, ok := action.InvocationFromContext(ctx)
			if !ok {
				return events.Output{}, errors.New("worker invocation context is required")
			}
			downstreamKey, err := invocation.DownstreamIdempotencyKey("resource-state-store")
			if err != nil {
				return events.Output{}, err
			}
			now := time.Now().UTC()
			details, _ := json.Marshal(map[string]string{"downstreamIdempotencyKey": downstreamKey})
			return events.Output{
				ActualStates: []events.ActualState{{
					ResourceID: input.ResourceID, Kind: input.Kind, State: input.State,
					ObservedAt: now, Details: details,
				}},
				Health: []events.Health{{ResourceID: input.ResourceID, Status: events.HealthUnknown, CheckedAt: now}},
			}, nil
		},
		func(_ context.Context, input recordResourceInput, output events.Output) error {
			if len(output.ActualStates) != 1 || output.ActualStates[0].ResourceID != input.ResourceID || output.ActualStates[0].State != input.State {
				return errors.New("recorded state does not match requested state")
			}
			return nil
		},
	)
	if err := registry.Register(definition); err != nil {
		return nil, worker.Worker{}, err
	}
	coreDefinition, err := coreactionapi.NewApplyAction(coreObjects)
	if err != nil {
		return nil, worker.Worker{}, err
	}
	if err := registry.Register(coreDefinition); err != nil {
		return nil, worker.Worker{}, err
	}
	repository, err := postgres.NewJobRepository(db)
	if err != nil {
		return nil, worker.Worker{}, err
	}
	versionBoundRepository, err := newVersionBoundJobRepository(repository)
	if err != nil {
		return nil, worker.Worker{}, err
	}
	state, err := postgres.NewOrchestrationState(db)
	if err != nil {
		return nil, worker.Worker{}, err
	}
	server, err := orchestrationapi.New(orchestrationapi.Config{
		Registry: registry, Jobs: versionBoundRepository, Persistence: state,
		Middleware: func(next http.Handler) http.Handler {
			return middleware(jobReconnectETagMiddleware(versionBoundCancellationMiddleware(next)))
		},
		Evaluator: policy.ThresholdEvaluator{
			PolicyID: "baseline-v1", ApprovalPermission: string(rbac.PermissionChangesApprove),
		},
		Protect: func(permission rbac.Permission, handler http.Handler) http.Handler {
			return identity.Authenticate(identity.Require(permission, rbac.GlobalScope())(handler))
		},
		Actor: func(request *http.Request) (string, bool) {
			principal, ok := identityapi.PrincipalFromContext(request.Context())
			return principal.Identity.ID, ok
		},
	})
	if err != nil {
		return nil, worker.Worker{}, err
	}
	runner := worker.Worker{
		ID: "embedded-worker", Repository: repository, Registry: registry,
		Allowlist:   worker.Set("resource.record", coreactionapi.ApplyActionName),
		Permissions: worker.Set(string(rbac.PermissionActionsExecute), string(rbac.PermissionCoreObjectsWrite)),
		LeaseTTL:    30 * time.Second, RetryPolicy: job.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute},
		Failures: worker.NoFailures{}, Now: time.Now,
	}
	return server, runner, nil
}

func runWorker(ctx context.Context, logger *slog.Logger, runtime *orchestrationapi.Server, runner worker.Worker) {
	workerTicker := time.NewTicker(250 * time.Millisecond)
	reconciliationTicker := time.NewTicker(2 * time.Second)
	defer workerTicker.Stop()
	defer reconciliationTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-workerTicker.C:
			completed, claimed, err := runner.RunOne(ctx, now.UTC())
			if claimed && completed.ID != "" {
				if reconcileErr := runtime.ReconcileJob(completed, time.Now().UTC()); reconcileErr != nil {
					logger.ErrorContext(ctx, "orchestration change reconciliation failed", "job_id", completed.ID, "error", reconcileErr)
				}
			}
			if err != nil {
				logger.ErrorContext(ctx, "orchestration job failed", "job_id", completed.ID, "error", err)
			} else if claimed {
				logger.InfoContext(ctx, "orchestration job completed", "job_id", completed.ID, "status", completed.Status)
			}
		case now := <-reconciliationTicker.C:
			if err := runtime.ReconcileTerminalJobs(ctx, now.UTC()); err != nil {
				logger.ErrorContext(ctx, "terminal orchestration job reconciliation sweep failed", "error", err)
			}
		}
	}
}
