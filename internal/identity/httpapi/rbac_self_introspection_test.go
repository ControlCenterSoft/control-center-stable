package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
	"control-center/internal/identity/security"
)

type toggleAuditLog struct {
	inner *audit.MemoryLog
	fail  bool
}

func (l *toggleAuditLog) Append(ctx context.Context, event audit.Event) error {
	if l.fail {
		return errors.New("audit unavailable")
	}
	return l.inner.Append(ctx, event)
}

type selfAccessFixture struct {
	server *Server
	log    *toggleAuditLog
}

func newSelfAccessFixture(t *testing.T) selfAccessFixture {
	t.Helper()
	store := auth.NewMemoryStore()
	memoryLog := audit.NewMemoryLog()
	log := &toggleAuditLog{inner: memoryLog}
	hasher := security.NewPasswordHasher()
	userHash, err := hasher.Hash("a secure test password")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapHash, err := hasher.HashBootstrapAdminPassword()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []auth.User{
		{ID: "viewer-1", Username: "viewer", DisplayName: "Viewer", PasswordHash: userHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "unbound-1", Username: "unbound", DisplayName: "Unbound", PasswordHash: userHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "admin-1", Username: "admin", DisplayName: "Administrator", PasswordHash: bootstrapHash, Enabled: true, PasswordChangeRequired: true, CreatedAt: time.Now().UTC()},
	} {
		if err := store.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	authService, err := auth.NewService(store, store, log, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := rbac.NewAuthorizer()
	for _, role := range rbac.BuiltinRoles() {
		if err := authorizer.RegisterRole(role); err != nil {
			t.Fatal(err)
		}
	}
	if err := authorizer.Bind(rbac.Binding{SubjectID: "viewer-1", RoleName: "viewer", Scope: rbac.GlobalScope()}); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Bind(rbac.Binding{SubjectID: "admin-1", RoleName: "administrator", Scope: rbac.GlobalScope()}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithSelfAccess(authService, authorizer, log, Config{})
	if err != nil {
		t.Fatal(err)
	}
	return selfAccessFixture{server: server, log: log}
}

func (f selfAccessFixture) login(t *testing.T, username, password string) *http.Cookie {
	t.Helper()
	body := bytes.NewBufferString(`{"username":"` + username + `","password":"` + password + `"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", result.Code, result.Body.String())
	}
	cookies := result.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies=%d, want 1", len(cookies))
	}
	return cookies[0]
}

func TestRBACSelfIntrospectionReturnsOnlyCurrentSubjectAndAudits(t *testing.T) {
	fixture := newSelfAccessFixture(t)
	cookie := fixture.login(t, "viewer", "a secure test password")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/identity/self/access?subject_id=admin-1", nil)
	request.RemoteAddr = "192.0.2.90:443"
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("self access status=%d body=%s", result.Code, result.Body.String())
	}
	var body struct {
		SubjectID string                `json:"subject_id"`
		Grants    []rbac.EffectiveGrant `json:"grants"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SubjectID != "viewer-1" {
		t.Fatalf("subject_id=%q, want viewer-1", body.SubjectID)
	}
	if len(body.Grants) != 1 || body.Grants[0].RoleName != "viewer" || body.Grants[0].Scope != rbac.GlobalScope() {
		t.Fatalf("unexpected grants: %#v", body.Grants)
	}
	if result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", result.Header().Get("Cache-Control"))
	}

	records := fixture.log.inner.Records()
	last := records[len(records)-1]
	if last.Action != "authorization.self_access_read" || last.Outcome != "success" || last.ActorID != "viewer-1" || last.SubjectID != "viewer-1" || last.SourceIP != "192.0.2.90" {
		t.Fatalf("unexpected self-access audit event: %#v", last)
	}
}

func TestRBACSelfIntrospectionAllowsEmptyOwnGrantSet(t *testing.T) {
	fixture := newSelfAccessFixture(t)
	cookie := fixture.login(t, "unbound", "a secure test password")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/identity/self/access", nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("self access status=%d body=%s", result.Code, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), `"subject_id":"unbound-1"`) || !strings.Contains(result.Body.String(), `"grants":[]`) {
		t.Fatalf("unexpected empty-grant response: %s", result.Body.String())
	}
}

func TestRBACSelfIntrospectionFailsClosedWhenAuditUnavailable(t *testing.T) {
	fixture := newSelfAccessFixture(t)
	cookie := fixture.login(t, "viewer", "a secure test password")
	fixture.log.fail = true

	request := httptest.NewRequest(http.MethodGet, "/api/v1/identity/self/access", nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("self access status=%d body=%s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "viewer") || strings.Contains(result.Body.String(), "permissions") {
		t.Fatalf("audit failure disclosed RBAC grants: %s", result.Body.String())
	}
}

func TestRBACSelfIntrospectionDoesNotBypassFirstLogin(t *testing.T) {
	fixture := newSelfAccessFixture(t)
	cookie := fixture.login(t, "admin", "admin")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/identity/self/access", nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("self access bypassed first-login boundary: status=%d body=%s", result.Code, result.Body.String())
	}
}
