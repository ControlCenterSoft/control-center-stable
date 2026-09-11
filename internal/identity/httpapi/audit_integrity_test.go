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

type auditIntegrityFixture struct {
	server *Server
	log    *audit.MemoryLog
}

func newAuditIntegrityFixture(t *testing.T) auditIntegrityFixture {
	t.Helper()
	store := auth.NewMemoryStore()
	log := audit.NewMemoryLog()
	hasher := security.NewPasswordHasher()
	hash, err := hasher.Hash("a secure test password")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapHash, err := hasher.HashBootstrapAdminPassword()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []auth.User{
		{ID: "auditor-1", Username: "auditor", DisplayName: "Auditor", PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "viewer-1", Username: "viewer", DisplayName: "Viewer", PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC()},
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
	for _, binding := range []rbac.Binding{
		{SubjectID: "auditor-1", RoleName: "auditor", Scope: rbac.GlobalScope()},
		{SubjectID: "viewer-1", RoleName: "viewer", Scope: rbac.GlobalScope()},
		{SubjectID: "admin-1", RoleName: "administrator", Scope: rbac.GlobalScope()},
	} {
		if err := authorizer.Bind(binding); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServerWithAuditIntegrity(authService, authorizer, log, Config{})
	if err != nil {
		t.Fatal(err)
	}
	return auditIntegrityFixture{server: server, log: log}
}

func (f auditIntegrityFixture) login(t *testing.T, username, password string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies=%d", len(cookies))
	}
	return cookies[0]
}

func TestAuditIntegrityEndpointReturnsVerifiedPrefixWithoutEventPayloads(t *testing.T) {
	f := newAuditIntegrityFixture(t)
	cookie := f.login(t, "auditor", "a secure test password")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/integrity", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", res.Header().Get("Cache-Control"))
	}
	var response struct {
		Status              string `json:"status"`
		EventsChecked       int    `json:"events_checked"`
		VerifiedThroughHash string `json:"verified_through_hash"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "verified" || response.EventsChecked < 1 || response.VerifiedThroughHash == "" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if strings.Contains(res.Body.String(), "identity.login") || strings.Contains(res.Body.String(), "password") {
		t.Fatalf("integrity response disclosed audit payload: %s", res.Body.String())
	}

	records := f.log.Records()
	last := records[len(records)-1]
	if last.Action != "audit.integrity_check" || last.Outcome != "success" || last.ActorID != "auditor-1" {
		t.Fatalf("unexpected audit evidence: %#v", last)
	}
	if response.VerifiedThroughHash != records[len(records)-2].Hash {
		t.Fatalf("verified_through_hash=%q want=%q", response.VerifiedThroughHash, records[len(records)-2].Hash)
	}
}

func TestAuditIntegrityEndpointRequiresAuditPermission(t *testing.T) {
	f := newAuditIntegrityFixture(t)
	cookie := f.login(t, "viewer", "a secure test password")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/integrity", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "permission_denied") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAuditIntegrityEndpointPreservesFirstLoginBoundary(t *testing.T) {
	f := newAuditIntegrityFixture(t)
	cookie := f.login(t, "admin", "admin")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/integrity", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "password_change_required") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

type failedIntegrityLog struct {
	*audit.MemoryLog
}

func (l *failedIntegrityLog) InspectChain(context.Context) (audit.IntegrityReport, error) {
	return audit.IntegrityReport{}, errors.New("synthetic chain corruption details")
}

func TestAuditIntegrityEndpointFailsClosedWithoutLeakingVerifierError(t *testing.T) {
	store := auth.NewMemoryStore()
	log := &failedIntegrityLog{MemoryLog: audit.NewMemoryLog()}
	hasher := security.NewPasswordHasher()
	hash, err := hasher.Hash("a secure test password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), auth.User{ID: "auditor-1", Username: "auditor", PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
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
	if err := authorizer.Bind(rbac.Binding{SubjectID: "auditor-1", RoleName: "auditor", Scope: rbac.GlobalScope()}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithAuditIntegrity(authService, authorizer, log, Config{})
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"username":"auditor","password":"a secure test password"}`)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	login.Header.Set("Content-Type", "application/json")
	loginResult := httptest.NewRecorder()
	server.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResult.Code, loginResult.Body.String())
	}
	cookie := loginResult.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/integrity", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "audit_integrity_failed") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "synthetic chain corruption details") {
		t.Fatalf("response leaked verifier error: %s", res.Body.String())
	}
}
