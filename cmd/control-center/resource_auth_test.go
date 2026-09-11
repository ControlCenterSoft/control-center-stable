package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"control-center/internal/corecontracts"
	coreobjectsapi "control-center/internal/corecontracts/httpapi"
	coreapi "control-center/internal/httpapi"
	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	identityapi "control-center/internal/identity/httpapi"
	"control-center/internal/identity/rbac"
	"control-center/internal/identity/security"
	"control-center/internal/resources"
)

const resourceAuthTestPassword = "a secure resource test password"

type resourceAuthFixture struct {
	handler http.Handler
}

func newResourceAuthFixture(t *testing.T) resourceAuthFixture {
	return newResourceAuthFixtureWithProductOptions(t)
}

func newResourceAuthFixtureWithProductOptions(t *testing.T, productOptions ...productHandlerOption) resourceAuthFixture {
	t.Helper()

	store := auth.NewMemoryStore()
	auditLog := audit.NewMemoryLog()
	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash(resourceAuthTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []auth.User{
		{ID: "admin-1", Username: "admin", DisplayName: "Administrator", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "operator-1", Username: "operator", DisplayName: "Operator", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "auditor-1", Username: "auditor", DisplayName: "Auditor", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "viewer-1", Username: "viewer", DisplayName: "Viewer", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "unbound-1", Username: "unbound", DisplayName: "Unbound", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
	} {
		if err := store.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}

	authService, err := auth.NewService(store, store, auditLog, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := rbac.NewAuthorizer()
	for _, role := range rbac.BuiltinRoles() {
		if err := authorizer.RegisterRole(role); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []rbac.Binding{
		{SubjectID: "admin-1", RoleName: "administrator", Scope: rbac.GlobalScope()},
		{SubjectID: "operator-1", RoleName: "operator", Scope: rbac.GlobalScope()},
		{SubjectID: "auditor-1", RoleName: "auditor", Scope: rbac.GlobalScope()},
		{SubjectID: "viewer-1", RoleName: "viewer", Scope: rbac.GlobalScope()},
	} {
		if err := authorizer.Bind(binding); err != nil {
			t.Fatal(err)
		}
	}

	identity, err := identityapi.NewServer(authService, authorizer, auditLog, identityapi.Config{
		InsecureCookiesForDevelopment: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	registry, err := resources.NewMemoryRegistry([]resources.Resource{{
		ID: "node-1", OrganizationID: "org-1", Kind: "node", Name: "Node 1",
		Status: "ready", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	resourceGuard := func(next http.Handler) http.Handler {
		return identity.Authenticate(identity.Require(rbac.PermissionResourcesRead, rbac.GlobalScope())(next))
	}
	core := coreapi.New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry,
		coreapi.WithResourceGuard(resourceGuard),
	)
	product := newProductHandler(identity, productOptions...)
	coreObjects, err := corecontracts.NewMemoryObjectRepository([]corecontracts.StoredObject{corecontracts.LegacyGlobalScopeObject(now)})
	if err != nil {
		t.Fatal(err)
	}
	coreObjectGuard := func(next http.Handler) http.Handler {
		return identity.Authenticate(identity.Require(rbac.PermissionCoreObjectsRead, rbac.GlobalScope())(next))
	}
	distributedCore := coreobjectsapi.New(slog.New(slog.NewTextHandler(io.Discard, nil)), coreObjects, coreObjectGuard)

	return resourceAuthFixture{handler: splitHandler{core: core.Handler(), identity: identity, distributedCore: distributedCore.Handler(), product: product}}
}

func TestDistributedCoreAPIRequiresAuthenticationAndPermission(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	tests := []struct {
		username string
		status   int
	}{
		{"", http.StatusUnauthorized},
		{"unbound", http.StatusForbidden},
		{"viewer", http.StatusOK},
		{"operator", http.StatusOK},
		{"admin", http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/core/objects", nil)
		if test.username != "" {
			request.AddCookie(fixture.login(t, test.username))
		}
		result := httptest.NewRecorder()
		fixture.handler.ServeHTTP(result, request)
		if result.Code != test.status {
			t.Errorf("user %q status=%d want=%d body=%s", test.username, result.Code, test.status, result.Body.String())
		}
	}
}

func (f resourceAuthFixture) login(t *testing.T, username string) *http.Cookie {
	t.Helper()
	body := []byte(`{"username":"` + username + `","password":"` + resourceAuthTestPassword + `"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	f.handler.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("login %q status = %d, want 200; body=%s", username, result.Code, result.Body.String())
	}
	cookies := result.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login %q returned %d cookies, want 1", username, len(cookies))
	}
	return cookies[0]
}

func TestResourceAPIRequiresAuthenticationAndResourcesRead(t *testing.T) {
	fixture := newResourceAuthFixture(t)

	tests := []struct {
		name       string
		username   string
		wantStatus int
	}{
		{name: "anonymous", wantStatus: http.StatusUnauthorized},
		{name: "unbound", username: "unbound", wantStatus: http.StatusForbidden},
		{name: "administrator", username: "admin", wantStatus: http.StatusOK},
		{name: "viewer", username: "viewer", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
			if test.username != "" {
				request.AddCookie(fixture.login(t, test.username))
			}
			result := httptest.NewRecorder()
			fixture.handler.ServeHTTP(result, request)
			if result.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", result.Code, test.wantStatus, result.Body.String())
			}
		})
	}
}

func TestPublicCoreEndpointsAndLoginRemainAvailable(t *testing.T) {
	fixture := newResourceAuthFixture(t)
	for _, path := range []string{"/health/live", "/health/ready", "/api/v1/version"} {
		result := httptest.NewRecorder()
		fixture.handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		if result.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, result.Code, result.Body.String())
		}
	}
	_ = fixture.login(t, "viewer")
}
