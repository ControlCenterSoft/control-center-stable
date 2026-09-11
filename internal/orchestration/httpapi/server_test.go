package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	commonapi "control-center/internal/httpapi"
	"control-center/internal/identity/rbac"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/change"
	orchestrationconfig "control-center/internal/orchestration/config"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
	"control-center/internal/orchestration/worker"
)

type testInput struct {
	Name string `json:"name"`
}
type apiFixture struct {
	handler     http.Handler
	server      *Server
	repository  job.Repository
	registry    *action.Registry
	persistence *testPersistence
}
type countingJobRepository struct {
	job.Repository
	mu   sync.Mutex
	gets int
}

func (r *countingJobRepository) Get(ctx context.Context, id string) (job.Job, error) {
	r.mu.Lock()
	r.gets++
	r.mu.Unlock()
	return r.Repository.Get(ctx, id)
}
func (r *countingJobRepository) getCount() int { r.mu.Lock(); defer r.mu.Unlock(); return r.gets }

type testPersistence struct {
	mu                sync.Mutex
	state             PersistedState
	failChangeUpdates int
	changeUpdates     int
}

func (p *testPersistence) Load(context.Context) (PersistedState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state, nil
}
func (p *testPersistence) CreateRevision(_ context.Context, actor, key, fingerprint string, content json.RawMessage, now time.Time) (PersistedRevision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, revision := range p.state.Revisions {
		if revision.IdempotencyKey == key {
			if revision.Fingerprint != fingerprint {
				return PersistedRevision{}, job.ErrIdempotencyConflict
			}
			return revision, nil
		}
	}
	revisionID := "rev-test-" + string(rune('a'+len(p.state.Revisions)))
	model, err := orchestrationconfig.NewRevision(revisionID, uint64(len(p.state.Revisions)+1), now, content)
	if err != nil {
		return PersistedRevision{}, err
	}
	revision := PersistedRevision{ID: revisionID, Sequence: model.Sequence(), Digest: model.Digest(), Content: json.RawMessage(model.Content()), CreatedAt: now, CreatedBy: actor, IdempotencyKey: key, Fingerprint: fingerprint}
	p.state.Revisions = append(p.state.Revisions, revision)
	return revision, nil
}
func (p *testPersistence) CreateChange(_ context.Context, requested PersistedChange) (PersistedChange, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, existing := range p.state.Changes {
		if existing.IdempotencyKey == requested.IdempotencyKey {
			if existing.Fingerprint != requested.Fingerprint {
				return PersistedChange{}, false, job.ErrIdempotencyConflict
			}
			return existing, false, nil
		}
	}
	p.state.Changes = append(p.state.Changes, requested)
	return requested, true, nil
}
func (p *testPersistence) UpdateChange(_ context.Context, updated PersistedChange) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.changeUpdates++
	if p.failChangeUpdates > 0 {
		p.failChangeUpdates--
		return errors.New("injected change persistence failure")
	}
	for i := range p.state.Changes {
		if p.state.Changes[i].Snapshot.ID == updated.Snapshot.ID {
			p.state.Changes[i] = updated
			return nil
		}
	}
	return errors.New("change not found")
}
func (p *testPersistence) changeUpdateCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changeUpdates
}
func (p *testPersistence) failNextChangeUpdates(count int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failChangeUpdates = count
}
func (p *testPersistence) changeState(id string) change.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, persisted := range p.state.Changes {
		if persisted.Snapshot.ID == id {
			return persisted.Snapshot.State
		}
	}
	return ""
}
func newAPIFixture(t *testing.T) apiFixture {
	return newAPIFixtureWith(t, &testPersistence{}, job.NewMemoryRepository())
}
func newAPIFixtureWith(t *testing.T, persistence *testPersistence, repository job.Repository) apiFixture {
	t.Helper()
	registry := action.NewRegistry()
	register := func(name string, risk policy.Risk) {
		t.Helper()
		definition := action.NewTyped(name, string(rbac.PermissionActionsExecute), risk, json.RawMessage(`{"type":"object","required":["name"]}`), func(ctx context.Context, input testInput) (events.Output, error) {
			invocation, ok := action.InvocationFromContext(ctx)
			if !ok {
				t.Fatal("action did not receive invocation context")
			}
			if _, err := invocation.DownstreamIdempotencyKey("test/store"); err != nil {
				t.Fatal(err)
			}
			return events.Output{ActualStates: []events.ActualState{{ResourceID: input.Name, Kind: "test", State: events.StatePresent, ObservedAt: time.Now().UTC()}}}, nil
		}, func(context.Context, testInput, events.Output) error { return nil })
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	register("test.medium", policy.RiskMedium)
	register("test.high", policy.RiskHigh)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := New(Config{Registry: registry, Jobs: repository, Persistence: persistence, Middleware: func(next http.Handler) http.Handler { return commonapi.Middleware(logger, next) }, Evaluator: policy.ThresholdEvaluator{PolicyID: "test-v1", ApprovalPermission: string(rbac.PermissionChangesApprove)}, Protect: func(permission rbac.Permission, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Actor") == "" {
				writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
				return
			}
			permissions := "," + r.Header.Get("X-Test-Permissions") + ","
			if !strings.Contains(permissions, ",*,") && !strings.Contains(permissions, ","+string(permission)+",") {
				writeError(w, r, http.StatusForbidden, "permission_denied", "Permission denied")
				return
			}
			next.ServeHTTP(w, r)
		})
	}, Actor: func(r *http.Request) (string, bool) { actor := r.Header.Get("X-Test-Actor"); return actor, actor != "" }})
	if err != nil {
		t.Fatal(err)
	}
	return apiFixture{handler: server.Handler(), server: server, repository: repository, registry: registry, persistence: persistence}
}
func (f apiFixture) request(t *testing.T, method, path, body, actor string, permissions ...rbac.Permission) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if actor != "" {
		request.Header.Set("X-Test-Actor", actor)
	}
	values := make([]string, len(permissions))
	for i, permission := range permissions {
		values[i] = string(permission)
	}
	request.Header.Set("X-Test-Permissions", strings.Join(values, ","))
	result := httptest.NewRecorder()
	f.handler.ServeHTTP(result, request)
	return result
}
func createQueuedTestChange(t *testing.T, fixture apiFixture, suffix string) changeView {
	t.Helper()
	revisionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/config/revisions", strings.NewReader(`{"content":{"generation":1}}`))
	revisionRequest.Header.Set("Content-Type", "application/json")
	revisionRequest.Header.Set("Idempotency-Key", "terminal-revision-"+suffix)
	revisionRequest.Header.Set("X-Test-Actor", "operator")
	revisionRequest.Header.Set("X-Test-Permissions", string(rbac.PermissionRevisionsWrite))
	revisionResult := httptest.NewRecorder()
	fixture.handler.ServeHTTP(revisionResult, revisionRequest)
	if revisionResult.Code != http.StatusCreated {
		t.Fatalf("create revision status=%d body=%s", revisionResult.Code, revisionResult.Body.String())
	}
	var revision revisionView
	if err := json.Unmarshal(revisionResult.Body.Bytes(), &revision); err != nil {
		t.Fatal(err)
	}
	changeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/changes", strings.NewReader(`{"action":"test.medium","input":{"name":"terminal-resource-`+suffix+`"},"revisionId":"`+revision.ID+`"}`))
	changeRequest.Header.Set("Content-Type", "application/json")
	changeRequest.Header.Set("Idempotency-Key", "terminal-change-"+suffix)
	changeRequest.Header.Set("If-Match-Revision", revision.ID)
	changeRequest.Header.Set("X-Test-Actor", "operator")
	changeRequest.Header.Set("X-Test-Permissions", string(rbac.PermissionChangesWrite))
	changeResult := httptest.NewRecorder()
	fixture.handler.ServeHTTP(changeResult, changeRequest)
	if changeResult.Code != http.StatusAccepted {
		t.Fatalf("create change status=%d body=%s", changeResult.Code, changeResult.Body.String())
	}
	var created changeView
	if err := json.Unmarshal(changeResult.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.State != change.StateQueued || created.JobID == "" {
		t.Fatalf("created change = %#v", created)
	}
	return created
}
func completeOneTestJob(t *testing.T, fixture apiFixture) job.Job {
	t.Helper()
	runner := worker.Worker{ID: "terminal-test-worker", Repository: fixture.repository, Registry: fixture.registry, Allowlist: worker.Set("test.medium"), Permissions: worker.Set(string(rbac.PermissionActionsExecute)), LeaseTTL: time.Second, RetryPolicy: job.RetryPolicy{BaseDelay: time.Millisecond}, Failures: worker.NoFailures{}}
	completed, claimed, err := runner.RunOne(context.Background(), time.Now().UTC())
	if err != nil || !claimed || completed.Status != job.StatusSucceeded {
		t.Fatalf("worker result=%#v claimed=%v err=%v", completed, claimed, err)
	}
	return completed
}
func reconciliationCandidateCount(server *Server) int {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return len(server.reconciliationCandidates)
}
func TestRoutesRequireAuthenticationAndRBAC(t *testing.T) {
	fixture := newAPIFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/config/revisions", strings.NewReader(`{"content":{"version":1}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "revision-1")
	request.Header.Set("X-Correlation-ID", "orchestration-auth-test")
	result := httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", result.Code)
	}
	if result.Header().Get("X-Correlation-ID") != "orchestration-auth-test" {
		t.Fatalf("correlation header = %q", result.Header().Get("X-Correlation-ID"))
	}
	if !strings.Contains(result.Body.String(), `"correlation_id":"orchestration-auth-test"`) {
		t.Fatalf("missing stable correlation envelope: %s", result.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/config/revisions", strings.NewReader(`{"content":{"version":1}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "revision-1")
	request.Header.Set("X-Test-Actor", "viewer")
	request.Header.Set("X-Test-Permissions", string(rbac.PermissionActionsRead))
	result = httptest.NewRecorder()
	fixture.handler.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("wrong permission status = %d body=%s", result.Code, result.Body.String())
	}
}
func TestOrchestrationStateSurvivesServerRestart(t *testing.T) {
	persistence := &testPersistence{}
	repository := job.NewMemoryRepository()
	first := newAPIFixtureWith(t, persistence, repository)
	revisionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/config/revisions", strings.NewReader(`{"content":{"generation":1}}`))
	revisionRequest.Header.Set("Content-Type", "application/json")
	revisionRequest.Header.Set("Idempotency-Key", "restart-revision")
	revisionRequest.Header.Set("X-Test-Actor", "operator")
	revisionRequest.Header.Set("X-Test-Permissions", string(rbac.PermissionRevisionsWrite))
	revisionResult := httptest.NewRecorder()
	first.handler.ServeHTTP(revisionResult, revisionRequest)
	if revisionResult.Code != http.StatusCreated {
		t.Fatalf("revision status=%d body=%s", revisionResult.Code, revisionResult.Body.String())
	}
	var revision revisionView
	if err := json.Unmarshal(revisionResult.Body.Bytes(), &revision); err != nil {
		t.Fatal(err)
	}
	changeBody := `{"action":"test.high","input":{"name":"restart-resource"},"revisionId":"` + revision.ID + `"}`
	changeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/changes", strings.NewReader(changeBody))
	changeRequest.Header.Set("Content-Type", "application/json")
	changeRequest.Header.Set("Idempotency-Key", "restart-change")
	changeRequest.Header.Set("If-Match-Revision", revision.ID)
	changeRequest.Header.Set("X-Test-Actor", "operator")
	changeRequest.Header.Set("X-Test-Permissions", string(rbac.PermissionChangesWrite))
	changeResult := httptest.NewRecorder()
	first.handler.ServeHTTP(changeResult, changeRequest)
	if changeResult.Code != http.StatusAccepted {
		t.Fatalf("change status=%d body=%s", changeResult.Code, changeResult.Body.String())
	}
	var original changeView
	if err := json.Unmarshal(changeResult.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	restarted := newAPIFixtureWith(t, persistence, repository)
	restarted.server.mu.RLock()
	restored := restarted.server.changes[original.ID]
	currentRevision := restarted.server.currentRevision
	restarted.server.mu.RUnlock()
	if restored == nil || restored.machine.Snapshot().State != change.StatePendingApproval || currentRevision != revision.ID {
		t.Fatalf("state was not restored: change=%#v revision=%q", restored, currentRevision)
	}
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/v1/changes", strings.NewReader(changeBody))
	replayRequest.Header = changeRequest.Header.Clone()
	changeResult = httptest.NewRecorder()
	restarted.handler.ServeHTTP(changeResult, replayRequest)
	if changeResult.Code != http.StatusAccepted || !strings.Contains(changeResult.Body.String(), original.ID) {
		t.Fatalf("idempotent replay after restart status=%d body=%s", changeResult.Code, changeResult.Body.String())
	}
}
func TestStartupRepairsChangeAfterCrashFollowingTerminalJobWrite(t *testing.T) {
	persistence := &testPersistence{}
	repository := job.NewMemoryRepository()
	first := newAPIFixtureWith(t, persistence, repository)
	created := createQueuedTestChange(t, first, "startup")
	if candidates := reconciliationCandidateCount(first.server); candidates != 1 {
		t.Fatalf("startup fixture candidates=%d want=1", candidates)
	}
	completed := completeOneTestJob(t, first)
	if completed.ID != created.JobID {
		t.Fatalf("completed job id=%q want=%q", completed.ID, created.JobID)
	}
	if durable := persistence.changeState(created.ID); durable != change.StateQueued {
		t.Fatalf("durable change before simulated restart=%s want=%s", durable, change.StateQueued)
	}
	restarted := newAPIFixtureWith(t, persistence, repository)
	if durable := persistence.changeState(created.ID); durable != change.StateSucceeded {
		t.Fatalf("durable change after startup repair=%s want=%s", durable, change.StateSucceeded)
	}
	if candidates := reconciliationCandidateCount(restarted.server); candidates != 0 {
		t.Fatalf("candidates after startup repair=%d want=0", candidates)
	}
	restarted.server.mu.RLock()
	snapshot := restarted.server.changes[created.ID].machine.Snapshot()
	restarted.server.mu.RUnlock()
	if snapshot.State != change.StateSucceeded {
		t.Fatalf("restored change state=%s want=%s", snapshot.State, change.StateSucceeded)
	}
	version := snapshot.Version
	updates := persistence.changeUpdateCount()
	if err := restarted.server.ReconcileTerminalJobs(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	restarted.server.mu.RLock()
	retried := restarted.server.changes[created.ID].machine.Snapshot()
	restarted.server.mu.RUnlock()
	if retried.State != change.StateSucceeded || retried.Version != version {
		t.Fatalf("idempotent startup reconciliation changed snapshot: before=%#v after=%#v", snapshot, retried)
	}
	if after := persistence.changeUpdateCount(); after != updates {
		t.Fatalf("consistent terminal change caused another persistence update: before=%d after=%d", updates, after)
	}
}
func TestRuntimeReconciliationRetriesFailedChangePersistenceWithoutRestart(t *testing.T) {
	fixture := newAPIFixture(t)
	created := createQueuedTestChange(t, fixture, "runtime")
	completed := completeOneTestJob(t, fixture)
	fixture.persistence.failNextChangeUpdates(1)
	if err := fixture.server.ReconcileJob(completed, time.Now().UTC()); err == nil {
		t.Fatal("expected injected reconciliation failure")
	}
	if candidates := reconciliationCandidateCount(fixture.server); candidates != 1 {
		t.Fatalf("candidates after failed persistence=%d want=1", candidates)
	}
	if durable := fixture.persistence.changeState(created.ID); durable != change.StateQueued {
		t.Fatalf("durable change after failed reconciliation=%s want=%s", durable, change.StateQueued)
	}
	if err := fixture.server.ReconcileTerminalJobs(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if durable := fixture.persistence.changeState(created.ID); durable != change.StateSucceeded {
		t.Fatalf("durable change after runtime retry=%s want=%s", durable, change.StateSucceeded)
	}
	if candidates := reconciliationCandidateCount(fixture.server); candidates != 0 {
		t.Fatalf("candidates after runtime repair=%d want=0", candidates)
	}
	fixture.server.mu.RLock()
	snapshot := fixture.server.changes[created.ID].machine.Snapshot()
	fixture.server.mu.RUnlock()
	if snapshot.State != change.StateSucceeded {
		t.Fatalf("runtime change state=%s want=%s", snapshot.State, change.StateSucceeded)
	}
	updates := fixture.persistence.changeUpdateCount()
	if err := fixture.server.ReconcileTerminalJobs(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if after := fixture.persistence.changeUpdateCount(); after != updates {
		t.Fatalf("consistent terminal change caused another persistence update: before=%d after=%d", updates, after)
	}
}
func TestReconciliationIndexSkipsLargeCleanTerminalHistory(t *testing.T) {
	repository := &countingJobRepository{Repository: job.NewMemoryRepository()}
	fixture := newAPIFixtureWith(t, &testPersistence{}, repository)
	created := createQueuedTestChange(t, fixture, "clean-history")
	completed := completeOneTestJob(t, fixture)
	if err := fixture.server.ReconcileJob(completed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if candidates := reconciliationCandidateCount(fixture.server); candidates != 0 {
		t.Fatalf("candidates after direct reconciliation=%d want=0", candidates)
	}
	fixture.server.mu.Lock()
	terminal := fixture.server.changes[created.ID]
	for i := 0; i < 10_000; i++ {
		fixture.server.changes[fmt.Sprintf("clean-terminal-%05d", i)] = terminal
	}
	fixture.server.mu.Unlock()
	gets := repository.getCount()
	if err := fixture.server.ReconcileTerminalJobs(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if after := repository.getCount(); after != gets {
		t.Fatalf("clean terminal history caused Jobs.Get calls: before=%d after=%d", gets, after)
	}
	if candidates := reconciliationCandidateCount(fixture.server); candidates != 0 {
		t.Fatalf("clean terminal history entered reconciliation index: %d", candidates)
	}
}
func TestOpenAPI03RoutesDriveDurableExecution(t *testing.T) {
	fixture := newAPIFixture(t)
	revisionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/config/revisions", strings.NewReader(`{"content":{"version":1}}`))
	revisionRequest.Header.Set("Content-Type", "application/json")
	revisionRequest.Header.Set("Idempotency-Key", "revision-1")
	revisionRequest.Header.Set("X-Test-Actor", "operator")
	revisionRequest.Header.Set("X-Test-Permissions", string(rbac.PermissionRevisionsWrite))
	revisionResult := httptest.NewRecorder()
	fixture.handler.ServeHTTP(revisionResult, revisionRequest)
	if revisionResult.Code != http.StatusCreated {
		t.Fatalf("create revision status=%d body=%s", revisionResult.Code, revisionResult.Body.String())
	}
	var revision revisionView
	if err := json.Unmarshal(revisionResult.Body.Bytes(), &revision); err != nil {
		t.Fatal(err)
	}
	actions := fixture.request(t, http.MethodGet, "/api/v1/actions", "", "operator", rbac.PermissionActionsRead)
	if actions.Code != http.StatusOK || !strings.Contains(actions.Body.String(), "test.medium") {
		t.Fatalf("actions status=%d body=%s", actions.Code, actions.Body.String())
	}
	createChange := func(actionName, key string) changeView {
		body := `{"action":"` + actionName + `","input":{"name":"resource-1"},"revisionId":"` + revision.ID + `"}`
		request := httptest.NewRequest(http.MethodPost, "/api/v1/changes", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("If-Match-Revision", revision.ID)
		request.Header.Set("X-Test-Actor", "operator")
		request.Header.Set("X-Test-Permissions", string(rbac.PermissionChangesWrite))
		result := httptest.NewRecorder()
		fixture.handler.ServeHTTP(result, request)
		if result.Code != http.StatusAccepted {
			t.Fatalf("create change status=%d body=%s", result.Code, result.Body.String())
		}
		var response changeView
		if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	medium := createChange("test.medium", "medium-1")
	if medium.State != "queued" || medium.JobID == "" {
		t.Fatalf("medium change = %#v", medium)
	}
	runner := worker.Worker{ID: "test-worker", Repository: fixture.repository, Registry: fixture.registry, Allowlist: worker.Set("test.medium", "test.high"), Permissions: worker.Set(string(rbac.PermissionActionsExecute)), LeaseTTL: time.Second, RetryPolicy: job.RetryPolicy{BaseDelay: time.Millisecond}, Failures: worker.NoFailures{}}
	completed, claimed, err := runner.RunOne(context.Background(), time.Now().UTC())
	if err != nil || !claimed || completed.Status != job.StatusSucceeded {
		t.Fatalf("worker result=%#v claimed=%v err=%v", completed, claimed, err)
	}
	if err := fixture.server.ReconcileJob(completed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.server.mu.RLock()
	completedChange := fixture.server.changes[medium.ID].machine.Snapshot()
	fixture.server.mu.RUnlock()
	if completedChange.State != "succeeded" {
		t.Fatalf("change state after worker completion = %s", completedChange.State)
	}
	jobResult := fixture.request(t, http.MethodGet, "/api/v1/jobs/"+medium.JobID, "", "auditor", rbac.PermissionJobsRead)
	if jobResult.Code != http.StatusOK || !strings.Contains(jobResult.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("job status=%d body=%s", jobResult.Code, jobResult.Body.String())
	}
	high := createChange("test.high", "high-1")
	if high.State != "pending_approval" || high.JobID != "" {
		t.Fatalf("high change before approval = %#v", high)
	}
	approval := httptest.NewRequest(http.MethodPost, "/api/v1/changes/"+high.ID+"/approvals", nil)
	approval.Header.Set("If-Match", "1")
	approval.Header.Set("X-Test-Actor", "approver")
	approval.Header.Set("X-Test-Permissions", string(rbac.PermissionChangesApprove))
	approvalResult := httptest.NewRecorder()
	fixture.handler.ServeHTTP(approvalResult, approval)
	if approvalResult.Code != http.StatusOK {
		t.Fatalf("approval status=%d body=%s", approvalResult.Code, approvalResult.Body.String())
	}
	var approved changeView
	if err := json.Unmarshal(approvalResult.Body.Bytes(), &approved); err != nil {
		t.Fatal(err)
	}
	if approved.State != "queued" || approved.JobID == "" {
		t.Fatalf("approved change = %#v", approved)
	}
	cancelled := fixture.request(t, http.MethodPost, "/api/v1/jobs/"+approved.JobID+"/cancel", "", "operator", rbac.PermissionJobsCancel)
	if cancelled.Code != http.StatusAccepted || !strings.Contains(cancelled.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel status=%d body=%s", cancelled.Code, cancelled.Body.String())
	}
}
