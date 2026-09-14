package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/incidents"
)

const maxIncidentRequestBytes int64 = 64 << 10

var (
	errInvalidJSONBody = errors.New("invalid incident JSON request")
	errRequestTooLarge = errors.New("incident request body too large")
)

type Service interface {
	Get(context.Context, string, string) (incidents.Incident, error)
	List(context.Context, string, incidents.ListQuery) (incidents.ListPage, error)
	Acknowledge(context.Context, string, string, incidents.AcknowledgeCommand) (incidents.Incident, error)
	Resolve(context.Context, string, string, incidents.ResolveCommand) (incidents.Incident, error)
	UpdateEvidence(context.Context, string, string, incidents.EvidenceUpdateCommand) (incidents.Incident, error)
}

type ActorResolver func(*http.Request) (string, bool)

type Option func(*server)

func WithClock(now func() time.Time) Option {
	return func(s *server) {
		if now != nil {
			s.now = now
		}
	}
}

type server struct {
	service Service
	actor   ActorResolver
	now     func() time.Time
}

func New(service Service, actor ActorResolver, options ...Option) http.Handler {
	s := &server{service: service, actor: actor, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/incidents", s.handleList)
	mux.HandleFunc("GET /api/v1/incidents/{incidentID}", s.handleGet)
	mux.HandleFunc("POST /api/v1/incidents/{incidentID}/acknowledge", s.handleAcknowledge)
	mux.HandleFunc("POST /api/v1/incidents/{incidentID}/resolve", s.handleResolve)
	mux.HandleFunc("POST /api/v1/incidents/{incidentID}/evidence", s.handleEvidence)
	return mux
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.resolveActor(w, r)
	if !ok {
		return
	}
	query, err := parseListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", "Incident query is invalid")
		return
	}
	page, err := s.service.List(r.Context(), actor, query)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.resolveActor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Query parameters are not supported")
		return
	}
	incident, err := s.service.Get(r.Context(), actor, r.PathValue("incidentID"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incident)
}

type acknowledgePayload struct {
	Precondition *corecontracts.ObjectPrecondition `json:"precondition"`
	Note         string                            `json:"note,omitempty"`
}

func (s *server) handleAcknowledge(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.resolveMutationRequest(w, r)
	if !ok {
		return
	}
	var payload acknowledgePayload
	if !decodePayload(w, r, &payload) {
		return
	}
	if payload.Precondition == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "precondition is required")
		return
	}
	incident, err := s.service.Acknowledge(r.Context(), actor, r.PathValue("incidentID"), incidents.AcknowledgeCommand{
		Precondition: *payload.Precondition,
		OccurredAt:   s.now().UTC(),
		Note:         payload.Note,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incident)
}

type resolvePayload struct {
	Precondition *corecontracts.ObjectPrecondition `json:"precondition"`
	Resolution   string                            `json:"resolution"`
	Evidence     []incidents.EvidenceRef           `json:"evidence,omitempty"`
}

func (s *server) handleResolve(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.resolveMutationRequest(w, r)
	if !ok {
		return
	}
	var payload resolvePayload
	if !decodePayload(w, r, &payload) {
		return
	}
	if payload.Precondition == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "precondition is required")
		return
	}
	incident, err := s.service.Resolve(r.Context(), actor, r.PathValue("incidentID"), incidents.ResolveCommand{
		Precondition: *payload.Precondition,
		OccurredAt:   s.now().UTC(),
		Resolution:   payload.Resolution,
		Evidence:     append([]incidents.EvidenceRef(nil), payload.Evidence...),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incident)
}

type evidencePayload struct {
	Precondition *corecontracts.ObjectPrecondition `json:"precondition"`
	Runbook      *incidents.RunbookRef             `json:"runbook,omitempty"`
	Evidence     []incidents.EvidenceRef           `json:"evidence,omitempty"`
	Note         string                            `json:"note"`
}

func (s *server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.resolveMutationRequest(w, r)
	if !ok {
		return
	}
	var payload evidencePayload
	if !decodePayload(w, r, &payload) {
		return
	}
	if payload.Precondition == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "precondition is required")
		return
	}
	incident, err := s.service.UpdateEvidence(r.Context(), actor, r.PathValue("incidentID"), incidents.EvidenceUpdateCommand{
		Precondition: *payload.Precondition,
		OccurredAt:   s.now().UTC(),
		Runbook:      payload.Runbook,
		Evidence:     append([]incidents.EvidenceRef(nil), payload.Evidence...),
		Note:         payload.Note,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, incident)
}

func (s *server) resolveMutationRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	actor, ok := s.resolveActor(w, r)
	if !ok {
		return "", false
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Query parameters are not supported")
		return "", false
	}
	if !hasJSONMediaType(r) {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type application/json is required")
		return "", false
	}
	return actor, true
}

func (s *server) resolveActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s == nil || s.service == nil || s.actor == nil {
		writeError(w, http.StatusServiceUnavailable, "incidents_unavailable", "Incident service is unavailable")
		return "", false
	}
	actor, ok := s.actor(r)
	if !ok || strings.TrimSpace(actor) == "" {
		writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		return "", false
	}
	return actor, true
}

func decodePayload(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxIncidentRequestBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body must be one JSON object")
		return false
	}
	if err := validateNoDuplicateJSONKeys(data); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body must contain unique JSON fields")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body must match the incident contract")
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body must contain one JSON object")
		return false
	}
	return true
}

func validateNoDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errInvalidJSONBody
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidJSONBody, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: object key", errInvalidJSONBody)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errInvalidJSONBody
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%w: duplicate field %q", errInvalidJSONBody, key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errInvalidJSONBody
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errInvalidJSONBody
		}
	default:
		return errInvalidJSONBody
	}
	return nil
}

func hasJSONMediaType(r *http.Request) bool {
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func parseListQuery(r *http.Request) (incidents.ListQuery, error) {
	values := r.URL.Query()
	allowed := map[string]struct{}{
		"limit": {}, "status": {}, "severity": {}, "scope_id": {}, "resource_kind": {}, "resource_id": {},
		"started_from": {}, "started_before": {}, "before_started_at": {}, "before_object_id": {},
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			return incidents.ListQuery{}, fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	query := incidents.ListQuery{}
	if value := values.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return incidents.ListQuery{}, err
		}
		query.Limit = limit
	}
	for _, value := range values["status"] {
		query.Statuses = append(query.Statuses, incidents.Status(value))
	}
	for _, value := range values["severity"] {
		query.Severities = append(query.Severities, incidents.Severity(value))
	}
	query.ScopeID = values.Get("scope_id")
	query.ResourceKind = values.Get("resource_kind")
	query.ResourceID = values.Get("resource_id")
	if value := values.Get("started_from"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return incidents.ListQuery{}, err
		}
		query.StartedFrom = &parsed
	}
	if value := values.Get("started_before"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return incidents.ListQuery{}, err
		}
		query.StartedBefore = &parsed
	}
	beforeStarted := values.Get("before_started_at")
	beforeID := values.Get("before_object_id")
	if (beforeStarted == "") != (beforeID == "") {
		return incidents.ListQuery{}, errors.New("complete cursor is required")
	}
	if beforeStarted != "" {
		parsed, err := time.Parse(time.RFC3339, beforeStarted)
		if err != nil {
			return incidents.ListQuery{}, err
		}
		query.Before = &incidents.ListCursor{StartedAt: parsed, ObjectID: beforeID}
	}
	return incidents.NormalizeListQuery(query)
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, incidents.ErrOperatorIdentityRequired):
		writeError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
	case errors.Is(err, incidents.ErrNotFound):
		writeError(w, http.StatusNotFound, "incident_not_found", "Incident was not found")
	case errors.Is(err, incidents.ErrOperatorAccessDenied):
		writeError(w, http.StatusForbidden, "incident_access_denied", "Incident access is denied")
	case errors.Is(err, incidents.ErrOperatorStepUpRequired):
		writeError(w, http.StatusPreconditionRequired, "step_up_required", "Fresh operator authentication is required")
	case errors.Is(err, corecontracts.ErrPreconditionFailed), errors.Is(err, incidents.ErrIncidentStateConflict), errors.Is(err, incidents.ErrIncidentNoChange):
		writeError(w, http.StatusConflict, "incident_conflict", "Incident state changed or the operation is not applicable")
	case errors.Is(err, corecontracts.ErrPreconditionRequired), errors.Is(err, corecontracts.ErrInvalidPrecondition), errors.Is(err, corecontracts.ErrInvalidTransition), errors.Is(err, incidents.ErrInvalidIncident), errors.Is(err, incidents.ErrInvalidTimeline), errors.Is(err, incidents.ErrInvalidEvidence), errors.Is(err, incidents.ErrInvalidAcknowledgement), errors.Is(err, incidents.ErrIncidentEvidenceConflict):
		writeError(w, http.StatusUnprocessableEntity, "invalid_incident_operation", "Incident operation is invalid")
	case errors.Is(err, incidents.ErrOperatorDependencyUnavailable):
		writeError(w, http.StatusServiceUnavailable, "incidents_unavailable", "Incident service is unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "incident_operation_failed", "Incident operation failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	response := errorResponse{}
	response.Error.Code = code
	response.Error.Message = message
	writeJSON(w, status, response)
}
