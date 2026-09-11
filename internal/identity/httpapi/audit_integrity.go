package httpapi

import (
	"errors"
	"net/http"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
)

// NewServerWithAuditIntegrity extends the cumulative Identity/RBAC server with
// a permission-gated read-only audit-chain integrity endpoint.
func NewServerWithAuditIntegrity(authService *auth.Service, authorizer SelfAccessAuthorizer, log audit.Logger, config Config) (*Server, error) {
	server, err := NewServerWithSelfAccess(authService, authorizer, log, config)
	if err != nil {
		return nil, err
	}
	checker, ok := log.(audit.IntegrityChecker)
	if !ok {
		return nil, errors.New("audit logger does not support integrity inspection")
	}
	server.mux.Handle(
		"GET /api/v1/audit/integrity",
		server.Authenticate(server.Require(rbac.PermissionAuditRead, rbac.GlobalScope())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.auditIntegrity(w, r, checker)
		}))),
	)
	return server, nil
}

func (s *Server) auditIntegrity(w http.ResponseWriter, r *http.Request, checker audit.IntegrityChecker) {
	report, err := checker.InspectChain(r.Context())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_integrity_failed", "Audit integrity could not be verified")
		return
	}

	principal, _ := PrincipalFromContext(r.Context())
	if err := s.audit.Append(r.Context(), audit.Event{
		Action: "audit.integrity_check", Outcome: "success", ActorID: principal.Identity.ID,
		SourceIP: remoteIP(r), Details: map[string]any{
			"events_checked":        report.EventsChecked,
			"verified_through_hash": report.HeadHash,
		},
	}); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_unavailable", "Audit integrity could not be reported")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":                "verified",
		"events_checked":        report.EventsChecked,
		"verified_through_hash": report.HeadHash,
	})
}
