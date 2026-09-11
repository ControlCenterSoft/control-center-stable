package worker

import (
	"context"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotAllowlisted   = errors.New("action is not worker-allowlisted")
	ErrPermissionDenied = errors.New("worker lacks action permission")
	ErrInjectedFailure  = errors.New("injected worker failure")
)

type FailurePoint string

const (
	FailureBeforeExecute FailurePoint = "before_execute"
	FailureAfterExecute  FailurePoint = "after_execute"
	FailureBeforeVerify  FailurePoint = "before_verify"
)

type FailureInjector interface {
	Inject(context.Context, FailurePoint, job.Job) error
}
type NoFailures struct{}

func (NoFailures) Inject(context.Context, FailurePoint, job.Job) error { return nil }

type Worker struct {
	ID          string
	Repository  job.Repository
	Registry    *action.Registry
	Allowlist   map[string]struct{}
	Permissions map[string]struct{}
	LeaseTTL    time.Duration
	RetryPolicy job.RetryPolicy
	Failures    FailureInjector
	Now         func() time.Time
}

func (w Worker) RunOne(ctx context.Context, now time.Time) (job.Job, bool, error) {
	if err := w.validate(); err != nil {
		return job.Job{}, false, err
	}
	claimed, ok, err := w.Repository.Claim(ctx, w.ID, now, w.LeaseTTL)
	if err != nil || !ok {
		return claimed, ok, err
	}
	clock := w.executionClock(now)
	executionContext, cancelExecution := context.WithCancel(ctx)
	stopRenewal := w.startLeaseRenewal(ctx, cancelExecution, claimed, clock)
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		return stopRenewal()
	}
	defer func() { _ = stop() }()
	fail := func(cause error, output events.Output) (job.Job, bool, error) {
		if renewErr := stop(); renewErr != nil {
			current, getErr := w.Repository.Get(ctx, claimed.ID)
			if getErr != nil {
				return job.Job{}, true, errors.Join(cause, renewErr, getErr)
			}
			return current, true, errors.Join(cause, renewErr)
		}
		output.AuditEvents = append(output.AuditEvents, events.AuditEvent{ID: fmt.Sprintf("audit-%s-%d-failed", claimed.ID, claimed.Attempt), OccurredAt: now.UTC(), Actor: w.ID, Action: claimed.ActionName, Outcome: "failed", CorrelationID: claimed.ChangeID})
		failed, storeErr := w.Repository.Fail(ctx, claimed.ID, claimed.Lease.Token, cause.Error(), output, w.RetryPolicy, clock())
		if storeErr != nil {
			return job.Job{}, true, errors.Join(cause, storeErr)
		}
		return failed, true, cause
	}
	definition, err := w.Registry.Resolve(claimed.ActionName)
	if err != nil {
		return fail(err, events.Output{})
	}
	if _, allowed := w.Allowlist[definition.Name]; !allowed {
		return fail(fmt.Errorf("%w: %s", ErrNotAllowlisted, definition.Name), events.Output{})
	}
	if _, permitted := w.Permissions[definition.Permission]; !permitted {
		return fail(fmt.Errorf("%w: %s", ErrPermissionDenied, definition.Permission), events.Output{})
	}
	if err := w.Failures.Inject(executionContext, FailureBeforeExecute, claimed); err != nil {
		return fail(fmt.Errorf("%w at %s: %v", ErrInjectedFailure, FailureBeforeExecute, err), events.Output{})
	}
	executionContext = action.WithInvocation(executionContext, action.Invocation{JobID: claimed.ID, ChangeID: claimed.ChangeID, ActionName: claimed.ActionName, IdempotencyKey: claimed.IdempotencyKey, Attempt: claimed.Attempt})
	output, err := definition.Execute(executionContext, claimed.Input)
	if err != nil {
		return fail(fmt.Errorf("execute %s: %w", definition.Name, err), output)
	}
	if err := w.Failures.Inject(executionContext, FailureAfterExecute, claimed); err != nil {
		return fail(fmt.Errorf("%w at %s: %v", ErrInjectedFailure, FailureAfterExecute, err), output)
	}
	current, err := w.Repository.Get(ctx, claimed.ID)
	if err != nil {
		return fail(fmt.Errorf("read cancellation state: %w", err), output)
	}
	if current.Status == job.StatusCancelRequested {
		return fail(errors.New("cancellation requested during execution"), output)
	}
	if err := w.Failures.Inject(executionContext, FailureBeforeVerify, claimed); err != nil {
		return fail(fmt.Errorf("%w at %s: %v", ErrInjectedFailure, FailureBeforeVerify, err), output)
	}
	if err := definition.Verify(executionContext, claimed.Input, output); err != nil {
		return fail(fmt.Errorf("verify %s: %w", definition.Name, err), output)
	}
	output.AuditEvents = append(output.AuditEvents, events.AuditEvent{ID: fmt.Sprintf("audit-%s-%d-succeeded", claimed.ID, claimed.Attempt), OccurredAt: now.UTC(), Actor: w.ID, Action: claimed.ActionName, Outcome: "succeeded", CorrelationID: claimed.ChangeID})
	if renewErr := stop(); renewErr != nil {
		current, getErr := w.Repository.Get(ctx, claimed.ID)
		if getErr != nil {
			return job.Job{}, true, errors.Join(renewErr, getErr)
		}
		return current, true, renewErr
	}
	completed, err := w.Repository.Succeed(ctx, claimed.ID, claimed.Lease.Token, output, clock())
	return completed, true, err
}
func (w Worker) executionClock(base time.Time) func() time.Time {
	if w.Now != nil {
		return func() time.Time { return w.Now().UTC() }
	}
	started := time.Now()
	return func() time.Time { return base.UTC().Add(time.Since(started)) }
}
func (w Worker) startLeaseRenewal(ctx context.Context, cancelExecution context.CancelFunc, claimed job.Job, clock func() time.Time) func() error {
	interval := w.LeaseTTL / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				cancelExecution()
				done <- ctx.Err()
				return
			case <-ticker.C:
				if _, err := w.Repository.RenewLease(ctx, claimed.ID, claimed.Lease.Token, clock(), w.LeaseTTL); err != nil {
					cancelExecution()
					done <- fmt.Errorf("renew job lease: %w", err)
					return
				}
			}
		}
	}()
	var once sync.Once
	var result error
	return func() error { once.Do(func() { close(stop); result = <-done; cancelExecution() }); return result }
}
func (w Worker) validate() error {
	if w.ID == "" || w.Repository == nil || w.Registry == nil || w.LeaseTTL <= 0 {
		return errors.New("worker id, repository, registry, and positive lease ttl are required")
	}
	if w.Failures == nil {
		return errors.New("failure injector must be configured; use NoFailures in production")
	}
	return nil
}
func Set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func Sorted(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
