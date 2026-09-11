package httpapi

import (
	"net/http"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
)

// SelfAccessAuthorizer is the read-only authorization boundary required by the
// self-introspection API. It combines ordinary enforcement with subject-scoped
// grant enumeration; neither capability permits querying another identity.
type SelfAccessAuthorizer interface {
	rbac.Checker
	rbac.Introspector
}

// NewServerWithSelfAccess enables the authenticated self-access endpoint in
// addition to the base identity routes.
func NewServerWithSelfAccess(authService *auth.Service, authorizer SelfAccessAuthorizer, log audit.Logger, config Config) (*Server, error) {
	server, err := NewServer(authService, authorizer, log, config)
	if err != nil {
		return nil, err
	}
	server.mux.Handle("GET /api/v1/identity/self/access", server.Authenticate(server.RequirePasswordCurrent(http.HandlerFunc(server.identitySelfAccess))))
	return server, nil
}

func (s *Server) identitySelfAccess(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	introspector, ok := s.authorizer.(rbac.Introspector)
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, "rbac_introspection_unavailable", "Authorization introspection is temporarily unavailable")
		return
	}

	grants, err := introspector.EffectiveGrants(r.Context(), principal.Identity.ID)
	if err != nil {
		_ = s.audit.Append(r.Context(), audit.Event{
			Action: "authorization.self_access_read", Outcome: "failed", ActorID: principal.Identity.ID,
			SubjectID: principal.Identity.ID, SourceIP: remoteIP(r), Details: map[string]any{"reason": "introspection_unavailable"},
		})
		writeError(w, r, http.StatusServiceUnavailable, "rbac_introspection_unavailable", "Authorization introspection is temporarily unavailable")
		return
	}

	if err := s.audit.Append(r.Context(), audit.Event{
		Action: "authorization.self_access_read", Outcome: "success", ActorID: principal.Identity.ID,
		SubjectID: principal.Identity.ID, SourceIP: remoteIP(r), Details: map[string]any{"grant_count": len(grants)},
	}); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_unavailable", "Authorization introspection is temporarily unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"subject_id": principal.Identity.ID, "grants": grants})
}
