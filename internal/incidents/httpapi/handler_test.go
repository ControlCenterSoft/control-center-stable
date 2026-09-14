package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"control-center/internal/incidents"
)

type serviceStub struct {
	getCalls         int
	listCalls        int
	acknowledgeCalls int
	resolveCalls     int
	evidenceCalls    int
	lastActor        string
	lastIncidentID   string
	lastAcknowledge  incidents.AcknowledgeCommand
	listErr          error
	acknowledgeErr   error
}

func (s *serviceStub) Get(context.Context, string, string) (incidents.Incident, error) {
	s.getCalls++
	return incidents.Incident{}, nil
}

func (s *serviceStub) List(_ context.Context, actor string, _ incidents.ListQuery) (incidents.ListPage, error) {
	s.listCalls++
	s.lastActor = actor
	return incidents.ListPage{}, s.listErr
}

func (s *serviceStub) Acknowledge(_ context.Context, actor, incidentID string, command incidents.AcknowledgeCommand) (incidents.Incident, error) {
	s.acknowledgeCalls++
	s.lastActor = actor
	s.lastIncidentID = incidentID
	s.lastAcknowledge = command
	return incidents.Incident{}, s.acknowledgeErr
}

func (s *serviceStub) Resolve(context.Context, string, string, incidents.ResolveCommand) (incidents.Incident, error) {
	s.resolveCalls++
	return incidents.Incident{}, nil
}

func (s *serviceStub) UpdateEvidence(context.Context, string, string, incidents.EvidenceUpdateCommand) (incidents.Incident, error) {
	s.evidenceCalls++
	return incidents.Incident{}, nil
}

func TestIncidentHTTPRequiresServerSideActor(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, func(*http.Request) (string, bool) { return "", false })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?scope_id=site:primary", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if service.listCalls != 0 {
		t.Fatalf("unauthenticated request reached service %d times", service.listCalls)
	}
}

func TestIncidentHTTPRejectsUnknownListQueryBeforeService(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, fixedActor("user:operator"))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?scope_id=site:primary&search=secret", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if service.listCalls != 0 {
		t.Fatalf("invalid query reached service %d times", service.listCalls)
	}
}

func TestIncidentHTTPRejectsClientActorField(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, fixedActor("user:operator"))
	body := []byte(`{"precondition":{"object_id":"incident:1","resource_version":"rv-1"},"actor_id":"user:admin"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/incident:1/acknowledge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if service.acknowledgeCalls != 0 {
		t.Fatalf("client actor field reached service %d times", service.acknowledgeCalls)
	}
}

func TestIncidentHTTPRejectsDuplicateNestedPreconditionField(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, fixedActor("user:operator"))
	body := []byte(`{"precondition":{"object_id":"incident:1","object_id":"incident:2","resource_version":"rv-1"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/incident:1/acknowledge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if service.acknowledgeCalls != 0 {
		t.Fatalf("duplicate JSON reached service %d times", service.acknowledgeCalls)
	}
}

func TestIncidentHTTPUsesServerClockAndAuthenticatedActor(t *testing.T) {
	service := &serviceStub{}
	now := time.Date(2026, 9, 12, 15, 22, 0, 0, time.UTC)
	handler := New(service, fixedActor("user:operator"), WithClock(func() time.Time { return now }))
	body := []byte(`{"precondition":{"object_id":"incident:1","resource_version":"rv-1"},"note":"accepted"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/incident:1/acknowledge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.acknowledgeCalls != 1 {
		t.Fatalf("acknowledge calls = %d, want 1", service.acknowledgeCalls)
	}
	if service.lastActor != "user:operator" || service.lastIncidentID != "incident:1" {
		t.Fatalf("service identity = %q/%q", service.lastActor, service.lastIncidentID)
	}
	if !service.lastAcknowledge.OccurredAt.Equal(now) {
		t.Fatalf("occurred_at = %s, want %s", service.lastAcknowledge.OccurredAt, now)
	}
}

func TestIncidentHTTPMapsStepUpToPreconditionRequired(t *testing.T) {
	service := &serviceStub{acknowledgeErr: incidents.ErrOperatorStepUpRequired}
	handler := New(service, fixedActor("user:operator"))
	body := []byte(`{"precondition":{"object_id":"incident:1","resource_version":"rv-1"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/incident:1/acknowledge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusPreconditionRequired)
	}
}

func TestIncidentHTTPMapsDeniedListWithoutStorageDetail(t *testing.T) {
	service := &serviceStub{listErr: incidents.ErrOperatorAccessDenied}
	handler := New(service, fixedActor("user:viewer"))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?scope_id=site:primary", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if !errors.Is(service.listErr, incidents.ErrOperatorAccessDenied) {
		t.Fatal("test setup lost access-denied sentinel")
	}
}

func fixedActor(actor string) ActorResolver {
	return func(*http.Request) (string, bool) { return actor, true }
}
