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

func TestJobOperationalReconnectRejectsStaleViewThenCancelsReviewedRunningVersion(t *testing.T) {
	base, repository, created := newCancellationTestRepository(t, "operational-reconnect")
	handler := jobReconnectETagMiddleware(versionBoundCancellationMiddleware(jobOperationalTestHandler(base, repository, created.ID)))

	initialRead := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+created.ID, nil)
	initialResult := httptest.NewRecorder()
	handler.ServeHTTP(initialResult, initialRead)
	if initialResult.Code != http.StatusOK {
		t.Fatalf("initial read status=%d body=%s", initialResult.Code, initialResult.Body.String())
	}
	initialETag := initialResult.Header().Get("ETag")
	if initialETag != `"`+strconv.FormatUint(created.Version, 10)+`"` {
		t.Fatalf("initial etag=%q version=%d", initialETag, created.Version)
	}
	if initialResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("initial cache-control=%q", initialResult.Header().Get("Cache-Control"))
	}

	claimed, ok, err := base.Claim(context.Background(), "worker-a", time.Unix(2000, 0).UTC(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=%#v ok=%v err=%v", claimed, ok, err)
	}
	if claimed.Version <= created.Version || claimed.Status != job.StatusRunning || claimed.Lease == nil {
		t.Fatalf("unexpected claimed job: %#v", claimed)
	}

	staleCancel := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	staleCancel.Header.Set("If-Match", initialETag)
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, staleCancel)
	if staleResult.Code != http.StatusPreconditionFailed || !strings.Contains(staleResult.Body.String(), "job_version_precondition_failed") {
		t.Fatalf("stale cancel status=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	unchanged, err := base.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Version != claimed.Version || unchanged.Lease == nil {
		t.Fatalf("stale cancel mutated running job: %#v", unchanged)
	}

	reconnect := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+created.ID, nil)
	reconnectResult := httptest.NewRecorder()
	handler.ServeHTTP(reconnectResult, reconnect)
	if reconnectResult.Code != http.StatusOK {
		t.Fatalf("reconnect status=%d body=%s", reconnectResult.Code, reconnectResult.Body.String())
	}
	currentETag := reconnectResult.Header().Get("ETag")
	if currentETag != `"`+strconv.FormatUint(claimed.Version, 10)+`"` {
		t.Fatalf("reconnect etag=%q version=%d", currentETag, claimed.Version)
	}
	if !strings.Contains(reconnectResult.Body.String(), `"status":"running"`) {
		t.Fatalf("reconnect did not return current durable running state: %s", reconnectResult.Body.String())
	}

	currentCancel := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/cancel", nil)
	currentCancel.Header.Set("If-Match", currentETag)
	currentResult := httptest.NewRecorder()
	handler.ServeHTTP(currentResult, currentCancel)
	if currentResult.Code != http.StatusAccepted {
		t.Fatalf("current cancel status=%d body=%s", currentResult.Code, currentResult.Body.String())
	}
	persisted, err := base.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != job.StatusCancelRequested || persisted.Version != claimed.Version+1 || persisted.Lease == nil {
		t.Fatalf("reviewed cancellation lost durable running semantics: %#v", persisted)
	}
	if currentResult.Header().Get("ETag") != `"`+strconv.FormatUint(persisted.Version, 10)+`"` {
		t.Fatalf("cancel response etag=%q persisted-version=%d", currentResult.Header().Get("ETag"), persisted.Version)
	}
}

func jobOperationalTestHandler(base job.Repository, cancellation job.Repository, jobID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/"+jobID:
			result, err := base.Get(r.Context(), jobID)
			if errors.Is(err, job.ErrNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				http.Error(w, "read failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/"+jobID+"/cancel":
			result, err := cancellation.RequestCancel(r.Context(), jobID, time.Unix(3000, 0).UTC())
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
		default:
			http.NotFound(w, r)
		}
	})
}
