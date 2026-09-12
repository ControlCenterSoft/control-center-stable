package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestVersionBoundCancellationRequiresAuthenticatedCallerBeforePrecondition(t *testing.T) {
	handler := versionBoundCancellationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-1/cancel", nil)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestVersionBoundCancellationFailsClosedWithoutReviewedVersion(t *testing.T) {
	base, repository, _ := newCancellationTestRepository(t, "missing-version")
	handler := versionBoundCancellationMiddleware(cancelRepositoryHandler(repository, "job-missing-version"))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-missing-version/cancel", nil)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusPreconditionRequired || !strings.Contains(result.Body.String(), "job_version_precondition_required") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	persisted, err := base.Get(context.Background(), "job-missing-version")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != job.StatusQueued || persisted.Version != 1 {
		t.Fatalf("job mutated without precondition: %#v", persisted)
	}
}

func TestVersionBoundCancellationRejectsStaleViewAndReconnectSucceeds(t *testing.T) {
	base, repository, created := newCancellationTestRepository(t, "stale-view")
	handler := versionBoundCancellationMiddleware(cancelRepositoryHandler(repository, created.ID))

	stale := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	stale.Header.Set("If-Match", `"2"`)
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed || !strings.Contains(staleResult.Body.String(), "job_version_precondition_failed") {
		t.Fatalf("stale status=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	unchanged, err := base.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Version != created.Version {
		t.Fatalf("stale request mutated job: %#v", unchanged)
	}

	current := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	current.Header.Set("If-Match", `"1"`)
	currentResult := httptest.NewRecorder()
	handler.ServeHTTP(currentResult, current)
	if currentResult.Code != http.StatusAccepted {
		t.Fatalf("current status=%d body=%s", currentResult.Code, currentResult.Body.String())
	}
	if currentResult.Header().Get("ETag") != `"2"` {
		t.Fatalf("etag=%q", currentResult.Header().Get("ETag"))
	}
	cancelled, err := base.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != job.StatusCancelled || cancelled.Version != created.Version+1 {
		t.Fatalf("cancelled=%#v", cancelled)
	}

	replay := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	replay.Header.Set("If-Match", `"2"`)
	replayResult := httptest.NewRecorder()
	handler.ServeHTTP(replayResult, replay)
	if replayResult.Code != http.StatusAccepted || replayResult.Header().Get("ETag") != `"2"` {
		t.Fatalf("terminal replay status=%d etag=%q body=%s", replayResult.Code, replayResult.Header().Get("ETag"), replayResult.Body.String())
	}
}

func TestVersionBoundCancellationPreservesRunningLeaseAndRejectsPreClaimView(t *testing.T) {
	base, repository, created := newCancellationTestRepository(t, "running")
	now := time.Unix(2000, 0).UTC()
	claimed, ok, err := base.Claim(context.Background(), "worker-a", now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=%#v ok=%v err=%v", claimed, ok, err)
	}
	if claimed.Version <= created.Version || claimed.Lease == nil {
		t.Fatalf("unexpected claimed job: %#v", claimed)
	}
	handler := versionBoundCancellationMiddleware(cancelRepositoryHandler(repository, created.ID))

	stale := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	stale.Header.Set("If-Match", `"`+strconv.FormatUint(created.Version, 10)+`"`)
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale running status=%d body=%s", staleResult.Code, staleResult.Body.String())
	}

	current := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	current.Header.Set("If-Match", `"`+strconv.FormatUint(claimed.Version, 10)+`"`)
	currentResult := httptest.NewRecorder()
	handler.ServeHTTP(currentResult, current)
	if currentResult.Code != http.StatusAccepted {
		t.Fatalf("current running status=%d body=%s", currentResult.Code, currentResult.Body.String())
	}
	persisted, err := base.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != job.StatusCancelRequested || persisted.Version != claimed.Version+1 || persisted.Lease == nil {
		t.Fatalf("running cancellation lost lease or state: %#v", persisted)
	}
}

func TestParseJobVersionPreconditionRejectsWeakWildcardAndLists(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "*", `W/"1"`, `"1","2"`, `"1`, `1"`, "abc"} {
		version, state := parseJobVersionPrecondition(value)
		if value == "" {
			if version != 0 || state != cancelVersionHeaderMissing {
				t.Fatalf("value=%q version=%d state=%d", value, version, state)
			}
			continue
		}
		if version != 0 || state != cancelVersionHeaderInvalid {
			t.Fatalf("value=%q version=%d state=%d", value, version, state)
		}
	}
	for _, value := range []string{"1", `"1"`, "42", `"42"`} {
		version, state := parseJobVersionPrecondition(value)
		if state != cancelVersionHeaderValid || version == 0 {
			t.Fatalf("value=%q version=%d state=%d", value, version, state)
		}
	}
}

func newCancellationTestRepository(t *testing.T, suffix string) (*job.MemoryRepository, *versionBoundJobRepository, job.Job) {
	t.Helper()
	base := job.NewMemoryRepository()
	now := time.Unix(1000, 0).UTC()
	created, _, err := base.Create(context.Background(), job.CreateRequest{
		ID:             "job-" + suffix,
		ChangeID:       "change-" + suffix,
		ActionName:     "resource.record",
		Input:          json.RawMessage(`{"resourceId":"node-1"}`),
		IdempotencyKey: "cancel-" + suffix,
		MaxAttempts:    3,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := newVersionBoundJobRepository(base)
	if err != nil {
		t.Fatal(err)
	}
	return base, repository, created
}

func cancelRepositoryHandler(repository job.Repository, jobID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := repository.RequestCancel(r.Context(), jobID, time.Unix(3000, 0).UTC())
		if errors.Is(err, job.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "cancel failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(result)
	})
}
