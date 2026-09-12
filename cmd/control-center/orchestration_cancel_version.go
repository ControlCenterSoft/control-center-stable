package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	commonapi "control-center/internal/httpapi"
	"control-center/internal/orchestration/job"
)

type cancelVersionHeaderState uint8

const (
	cancelVersionHeaderMissing cancelVersionHeaderState = iota
	cancelVersionHeaderInvalid
	cancelVersionHeaderValid
)

type cancelVersionContextKey struct{}

type cancelVersionRequest struct {
	headerState     cancelVersionHeaderState
	expectedVersion uint64
	overrideStatus  int
	overrideCode    string
	overrideMessage string
	responseVersion uint64
}

type versionBoundJobRepository struct {
	job.Repository
	versioned job.VersionedCancellationRepository
}

func newVersionBoundJobRepository(repository job.Repository) (*versionBoundJobRepository, error) {
	versioned, ok := repository.(job.VersionedCancellationRepository)
	if !ok {
		return nil, errors.New("job repository must support version-bound cancellation")
	}
	return &versionBoundJobRepository{Repository: repository, versioned: versioned}, nil
}

func (r *versionBoundJobRepository) RequestCancel(ctx context.Context, id string, now time.Time) (job.Job, error) {
	request, ok := ctx.Value(cancelVersionContextKey{}).(*cancelVersionRequest)
	if !ok || request == nil {
		return job.Job{}, errors.New("version-bound cancellation HTTP context is required")
	}
	switch request.headerState {
	case cancelVersionHeaderMissing:
		request.reject(http.StatusPreconditionRequired, "job_version_precondition_required", "If-Match with the reviewed Job version is required")
		return job.Job{}, errors.New("job cancellation version precondition is required")
	case cancelVersionHeaderInvalid:
		request.reject(http.StatusBadRequest, "invalid_job_version_precondition", "If-Match must contain one positive durable Job version")
		return job.Job{}, errors.New("job cancellation version precondition is invalid")
	case cancelVersionHeaderValid:
	default:
		return job.Job{}, errors.New("unknown job cancellation version precondition state")
	}

	result, err := r.versioned.RequestCancelIfVersion(ctx, id, request.expectedVersion, now)
	if errors.Is(err, job.ErrVersionConflict) {
		request.reject(http.StatusPreconditionFailed, "job_version_precondition_failed", "Job changed after it was reviewed; reload the Job before cancelling")
		return job.Job{}, err
	}
	if err == nil {
		request.responseVersion = result.Version
	}
	return result, err
}

func (r *cancelVersionRequest) reject(status int, code, message string) {
	r.overrideStatus = status
	r.overrideCode = code
	r.overrideMessage = message
}

func versionBoundCancellationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isJobCancellationRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		version, state := parseJobVersionPrecondition(r.Header.Get("If-Match"))
		requestState := &cancelVersionRequest{headerState: state, expectedVersion: version}
		r = r.WithContext(context.WithValue(r.Context(), cancelVersionContextKey{}, requestState))

		buffer := newBufferedResponse()
		next.ServeHTTP(buffer, r)
		if requestState.overrideStatus != 0 {
			commonapi.WriteError(w, r, requestState.overrideStatus, requestState.overrideCode, requestState.overrideMessage)
			return
		}
		if requestState.responseVersion > 0 {
			buffer.Header().Set("ETag", `"`+strconv.FormatUint(requestState.responseVersion, 10)+`"`)
		}
		buffer.flushTo(w)
	})
}

func isJobCancellationRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "jobs" && parts[3] != "" && parts[4] == "cancel"
}

func parseJobVersionPrecondition(raw string) (uint64, cancelVersionHeaderState) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, cancelVersionHeaderMissing
	}
	if value == "*" || strings.HasPrefix(strings.ToLower(value), "w/") || strings.Contains(value, ",") {
		return 0, cancelVersionHeaderInvalid
	}
	if strings.HasPrefix(value, `"`) || strings.HasSuffix(value, `"`) {
		if len(value) < 3 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
			return 0, cancelVersionHeaderInvalid
		}
		value = value[1 : len(value)-1]
	}
	version, err := strconv.ParseUint(value, 10, 64)
	if err != nil || version == 0 {
		return 0, cancelVersionHeaderInvalid
	}
	return version, cancelVersionHeaderValid
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (w *bufferedResponse) Header() http.Header { return w.header }

func (w *bufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponse) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

func (w *bufferedResponse) flushTo(destination http.ResponseWriter) {
	for key, values := range w.header {
		for _, value := range values {
			destination.Header().Add(key, value)
		}
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	_, _ = io.Copy(destination, &w.body)
}
