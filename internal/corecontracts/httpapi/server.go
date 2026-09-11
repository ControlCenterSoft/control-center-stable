// Package httpapi exposes authenticated read-only views of distributed core
// objects. Writes are intentionally available only through Change and Job.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"control-center/internal/corecontracts"
	commonapi "control-center/internal/httpapi"
)

type Server struct {
	logger     *slog.Logger
	repository corecontracts.ObjectRepository
	handler    http.Handler
}

// New constructs the read API. A missing guard fails closed with 401.
func New(logger *slog.Logger, repository corecontracts.ObjectRepository, guard func(http.Handler) http.Handler) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{logger: logger, repository: repository}
	protect := guard
	if protect == nil {
		protect = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				commonapi.WriteError(w, r, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication is required")
			})
		}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/core/objects", protect(http.HandlerFunc(server.listObjects)))
	mux.Handle("GET /api/v1/core/objects/{objectId}", protect(http.HandlerFunc(server.getObject)))
	mux.Handle("GET /api/v1/core/topology", protect(http.HandlerFunc(server.getTopology)))
	server.handler = mux
	return server
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) listObjects(w http.ResponseWriter, r *http.Request) {
	if !knownQuery(r, "object_type", "scope_id", "owner_scope") {
		commonapi.WriteError(w, r, http.StatusBadRequest, "INVALID_QUERY", "unsupported query parameter")
		return
	}
	filter := corecontracts.ObjectFilter{
		ObjectType: corecontracts.ObjectType(strings.TrimSpace(r.URL.Query().Get("object_type"))),
		ScopeID:    strings.TrimSpace(r.URL.Query().Get("scope_id")),
		OwnerScope: strings.TrimSpace(r.URL.Query().Get("owner_scope")),
	}
	if filter.ObjectType != "" && !filter.ObjectType.Valid() {
		commonapi.WriteError(w, r, http.StatusBadRequest, "INVALID_OBJECT_TYPE", "object_type is not supported")
		return
	}
	for _, identifier := range []string{filter.ScopeID, filter.OwnerScope} {
		if identifier != "" {
			if err := corecontracts.ValidateObjectIdentifier(identifier); err != nil {
				commonapi.WriteError(w, r, http.StatusBadRequest, "INVALID_SCOPE_ID", "scope filter is malformed")
				return
			}
		}
	}
	if s.repository == nil {
		commonapi.WriteError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "distributed core repository is not initialized")
		return
	}
	objects, err := s.repository.List(r.Context(), filter)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list distributed core objects", "error", err)
		commonapi.WriteError(w, r, http.StatusInternalServerError, "CORE_OBJECT_READ_FAILED", "unable to read distributed core objects")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items []corecontracts.StoredObject `json:"items"`
		Count int                          `json:"count"`
	}{Items: objects, Count: len(objects)})
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		commonapi.WriteError(w, r, http.StatusBadRequest, "INVALID_QUERY", "query parameters are not supported")
		return
	}
	objectID := r.PathValue("objectId")
	if err := corecontracts.ValidateObjectIdentifier(objectID); err != nil {
		commonapi.WriteError(w, r, http.StatusBadRequest, "INVALID_OBJECT_ID", "object id is malformed")
		return
	}
	if s.repository == nil {
		commonapi.WriteError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "distributed core repository is not initialized")
		return
	}
	object, err := s.repository.Get(r.Context(), objectID)
	if errors.Is(err, corecontracts.ErrObjectNotFound) {
		commonapi.WriteError(w, r, http.StatusNotFound, "CORE_OBJECT_NOT_FOUND", "distributed core object was not found")
		return
	}
	if err != nil {
		s.logger.ErrorContext(r.Context(), "get distributed core object", "object_id", objectID, "error", err)
		commonapi.WriteError(w, r, http.StatusInternalServerError, "CORE_OBJECT_READ_FAILED", "unable to read distributed core object")
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) getTopology(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		commonapi.WriteError(w, r, http.StatusBadRequest, "INVALID_QUERY", "query parameters are not supported")
		return
	}
	if s.repository == nil {
		commonapi.WriteError(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "distributed core repository is not initialized")
		return
	}
	objects, err := s.repository.List(r.Context(), corecontracts.ObjectFilter{})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "read distributed core topology", "error", err)
		commonapi.WriteError(w, r, http.StatusInternalServerError, "CORE_TOPOLOGY_READ_FAILED", "unable to read distributed core topology")
		return
	}
	response := struct {
		Scopes            []corecontracts.StoredObject `json:"scopes"`
		Sites             []corecontracts.StoredObject `json:"sites"`
		ManagementZones   []corecontracts.StoredObject `json:"management_zones"`
		NetworkZones      []corecontracts.StoredObject `json:"network_zones"`
		NetworkInterfaces []corecontracts.StoredObject `json:"network_interfaces"`
	}{
		Scopes:            make([]corecontracts.StoredObject, 0),
		Sites:             make([]corecontracts.StoredObject, 0),
		ManagementZones:   make([]corecontracts.StoredObject, 0),
		NetworkZones:      make([]corecontracts.StoredObject, 0),
		NetworkInterfaces: make([]corecontracts.StoredObject, 0),
	}
	for _, object := range objects {
		switch object.ObjectType {
		case corecontracts.ObjectScope:
			response.Scopes = append(response.Scopes, object)
		case corecontracts.ObjectSite:
			response.Sites = append(response.Sites, object)
		case corecontracts.ObjectManagementZone:
			response.ManagementZones = append(response.ManagementZones, object)
		case corecontracts.ObjectNetworkZone:
			response.NetworkZones = append(response.NetworkZones, object)
		case corecontracts.ObjectNetworkInterface:
			response.NetworkInterfaces = append(response.NetworkInterfaces, object)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func knownQuery(r *http.Request, allowed ...string) bool {
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for key := range r.URL.Query() {
		if _, ok := known[key]; !ok {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
