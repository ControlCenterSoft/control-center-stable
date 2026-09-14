package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"control-center/internal/identity/auth"
)

func TestAuthenticatedActorIDUsesPrincipalContextOnly(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/incidents", nil)
	ctx := context.WithValue(req.Context(), principalKey, Principal{Identity: auth.Identity{ID: "operator-1"}})
	req = req.WithContext(ctx)

	actorID, ok := AuthenticatedActorID(req)
	if !ok || actorID != "operator-1" {
		t.Fatalf("unexpected actor resolution: %q %v", actorID, ok)
	}
}

func TestAuthenticatedActorIDFailsClosedWithoutPrincipal(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/incidents", nil)
	if actorID, ok := AuthenticatedActorID(req); ok || actorID != "" {
		t.Fatalf("missing principal must fail closed: %q %v", actorID, ok)
	}
}

func TestAuthenticatedActorIDFailsClosedDuringMandatoryPasswordChange(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/incidents", nil)
	ctx := context.WithValue(req.Context(), principalKey, Principal{
		Identity:               auth.Identity{ID: "operator-1"},
		PasswordChangeRequired: true,
	})
	req = req.WithContext(ctx)

	if actorID, ok := AuthenticatedActorID(req); ok || actorID != "" {
		t.Fatalf("password-change gate must fail closed: %q %v", actorID, ok)
	}
}
