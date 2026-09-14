package httpapi

import (
	"net/http"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
)

// NewServerWithAuditExport extends the cumulative Identity/RBAC server with the
// qualified bounded Audit CSV export endpoint and the read-only browser Audit
// surface. Both routes reuse the existing global audit.events.read permission
// and mandatory current-password boundary.
func NewServerWithAuditExport(authService *auth.Service, authorizer SelfAccessAuthorizer, log audit.Logger, config Config) (*Server, error) {
	server, err := NewServerWithAuditIntegrity(authService, authorizer, log, config)
	if err != nil {
		return nil, err
	}
	server.mux.Handle(
		"GET /api/v1/audit/events/export",
		server.Authenticate(server.Require(rbac.PermissionAuditRead, rbac.GlobalScope())(http.HandlerFunc(server.auditEventsExport))),
	)
	server.mux.Handle(
		"GET /audit",
		server.AuthenticateWeb(server.RequireWeb(rbac.PermissionAuditRead, rbac.GlobalScope())(http.HandlerFunc(server.webAudit))),
	)
	return server, nil
}
