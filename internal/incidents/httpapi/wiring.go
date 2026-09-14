package httpapi

import (
	"errors"
	"net/http"

	identityhttpapi "control-center/internal/identity/httpapi"
)

// NewAuthenticated composes incident HTTP routes with the canonical Control
// Center session and first-login password-change gates. OperatorService remains
// responsible for scoped RBAC authorization, so authentication cannot be
// mistaken for permission to read or mutate an incident.
func NewAuthenticated(identityServer *identityhttpapi.Server, service Service, options ...Option) (http.Handler, error) {
	if identityServer == nil || service == nil {
		return nil, errors.New("identity server and incident service are required")
	}
	incidentHandler := New(service, identityhttpapi.AuthenticatedActorID, options...)
	return identityServer.AuthenticatedCurrentPassword(incidentHandler), nil
}
