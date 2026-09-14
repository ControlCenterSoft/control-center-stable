package httpapi

import (
	"net/http"
	"strings"

	"control-center/internal/identity/rbac"
)

// AuthenticatedActorID resolves the server-authenticated Control Center
// identity from request context. It is intended for application handlers that
// are composed behind Server.AuthenticatedCurrentPassword; it never accepts an
// actor identifier from headers, query parameters, or request payloads.
func AuthenticatedActorID(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.PasswordChangeRequired {
		return "", false
	}
	actorID := strings.TrimSpace(principal.Identity.ID)
	if actorID == "" {
		return "", false
	}
	return actorID, true
}

// AuthorizationChecker exposes the already-configured read-only RBAC decision
// boundary for composing application services. It does not expose role/binding
// mutation, persistence internals, or an authorization bypass.
func (s *Server) AuthorizationChecker() (rbac.Checker, bool) {
	if s == nil || s.authorizer == nil {
		return nil, false
	}
	return s.authorizer, true
}

// AuthenticatedCurrentPassword composes an external application handler with
// the same session authentication and mandatory first-login password-change
// gate used by built-in identity routes. RBAC remains the downstream handler's
// responsibility because its target scope can be resource-specific.
func (s *Server) AuthenticatedCurrentPassword(next http.Handler) http.Handler {
	if s == nil || next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, http.StatusServiceUnavailable, "route_unavailable", "Route is unavailable")
		})
	}
	return s.Authenticate(s.RequirePasswordCurrent(next))
}
