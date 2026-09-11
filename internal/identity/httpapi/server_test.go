package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	commonapi "control-center/internal/httpapi"
	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
	"control-center/internal/identity/security"
)

type httpFixture struct {
	server *Server
	log    *audit.MemoryLog
}

func TestIdentityErrorCorrelationMatchesCommonBoundary(t *testing.T) {
	fixture := newHTTPFixture(t)
	handler := commonapi.Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), fixture.server)

	for _, suppliedCorrelationID := range []string{"identity-request-42", ""} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		if suppliedCorrelationID != "" {
			request.Header.Set("X-Correlation-ID", suppliedCorrelationID)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
		}
		var envelope struct {
			Error struct {
				CorrelationID string `json:"correlation_id"`
			} `json:"error"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		headerCorrelationID := result.Header().Get("X-Correlation-ID")
		if headerCorrelationID == "" || envelope.Error.CorrelationID != headerCorrelationID {
			t.Fatalf("header correlation %q != body correlation %q", headerCorrelationID, envelope.Error.CorrelationID)
		}
		if suppliedCorrelationID != "" && headerCorrelationID != suppliedCorrelationID {
			t.Fatalf("header correlation %q, want supplied %q", headerCorrelationID, suppliedCorrelationID)
		}
	}
}

func newHTTPFixture(t *testing.T) httpFixture {
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
		{ID: "viewer-1", Username: "viewer", DisplayName: "Viewer", PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "unbound-1", Username: "unbound", DisplayName: "Unbound", PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC()},
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
	server, err := NewServer(authService, authorizer, log, Config{})
	if err != nil {
		t.Fatal(err)
	}
	return httpFixture{server: server, log: log}
}

func (f httpFixture) login(t *testing.T, username string) *http.Cookie {
	t.Helper()
	body := []byte(`{"username":"` + username + `","password":"a secure test password"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected session cookie, got %d", len(cookies))
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie lacks security attributes")
	}
	if strings.Contains(res.Body.String(), cookies[0].Value) || strings.Contains(res.Body.String(), "a secure test password") {
		t.Fatal("login response disclosed a credential or token")
	}
	return cookies[0]
}

func TestLoginSessionIdentityAndLogout(t *testing.T) {
	f := newHTTPFixture(t)
	cookie := f.login(t, "viewer")
	for _, path := range []string{"/api/v1/auth/session", "/api/v1/identity/self", "/api/v1/system/overview"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		f.server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(cookie)
	logoutResult := httptest.NewRecorder()
	f.server.ServeHTTP(logoutResult, logout)
	if logoutResult.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d", logoutResult.Code)
	}
	after := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	after.AddCookie(cookie)
	afterResult := httptest.NewRecorder()
	f.server.ServeHTTP(afterResult, after)
	if afterResult.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie status=%d", afterResult.Code)
	}
}

func TestAuthenticationAndAuthorizationAreDistinct(t *testing.T) {
	f := newHTTPFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/overview", nil)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", result.Code)
	}

	cookie := f.login(t, "unbound")
	request = httptest.NewRequest(http.MethodGet, "/api/v1/system/overview", nil)
	request.AddCookie(cookie)
	result = httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("unbound identity status=%d body=%s", result.Code, result.Body.String())
	}
	records := f.log.Records()
	if records[len(records)-1].Action != "authorization.check" || records[len(records)-1].Outcome != "denied" {
		t.Fatal("authorization denial was not audited")
	}
}

func TestInvalidLoginDoesNotEnumerateUsers(t *testing.T) {
	f := newHTTPFixture(t)
	statuses := make([]int, 0, 2)
	messages := make([]string, 0, 2)
	for _, username := range []string{"viewer", "does-not-exist"} {
		body, _ := json.Marshal(map[string]string{"username": username, "password": "wrong but long password"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		f.server.ServeHTTP(res, req)
		statuses = append(statuses, res.Code)
		messages = append(messages, res.Body.String())
	}
	if statuses[0] != http.StatusUnauthorized || statuses[1] != statuses[0] || messages[0] != messages[1] {
		t.Fatalf("login responses differ: status=%v body=%q", statuses, messages)
	}
}

func TestLoginRejectsUnknownFieldsAndWrongContentType(t *testing.T) {
	f := newHTTPFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"viewer","password":"a secure test password","admin":true}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", res.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("username=viewer"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res = httptest.NewRecorder()
	f.server.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status=%d", res.Code)
	}
}

func TestFirstLoginAllowsOnlyPasswordChangeFlow(t *testing.T) {
	f := newHTTPFixture(t)
	loginBody := strings.NewReader(`{"username":"admin","password":"admin"}`)
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResult := httptest.NewRecorder()
	f.server.ServeHTTP(loginResult, loginRequest)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("bootstrap login status=%d body=%s", loginResult.Code, loginResult.Body.String())
	}
	var loginResponse struct {
		PasswordChangeRequired bool `json:"password_change_required"`
	}
	if err := json.Unmarshal(loginResult.Body.Bytes(), &loginResponse); err != nil || !loginResponse.PasswordChangeRequired {
		t.Fatalf("bootstrap login flag=%v err=%v", loginResponse.PasswordChangeRequired, err)
	}
	cookies := loginResult.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bootstrap login cookies=%d", len(cookies))
	}
	cookie := cookies[0]

	for _, path := range []string{"/api/v1/identity/self", "/api/v1/system/overview"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookie)
		result := httptest.NewRecorder()
		f.server.ServeHTTP(result, request)
		if result.Code != http.StatusForbidden || !strings.Contains(result.Body.String(), "password_change_required") {
			t.Fatalf("protected %s status=%d body=%s", path, result.Code, result.Body.String())
		}
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRequest.AddCookie(cookie)
	sessionResult := httptest.NewRecorder()
	f.server.ServeHTTP(sessionResult, sessionRequest)
	if sessionResult.Code != http.StatusOK || !strings.Contains(sessionResult.Body.String(), `"password_change_required":true`) {
		t.Fatalf("session status=%d body=%s", sessionResult.Code, sessionResult.Body.String())
	}

	shortRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"admin","new_password":"short"}`))
	shortRequest.Header.Set("Content-Type", "application/json")
	shortRequest.AddCookie(cookie)
	shortResult := httptest.NewRecorder()
	f.server.ServeHTTP(shortResult, shortRequest)
	if shortResult.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short password status=%d body=%s", shortResult.Code, shortResult.Body.String())
	}

	const replacement = "a new compliant admin password"
	changeBody, err := json.Marshal(map[string]string{"current_password": "admin", "new_password": replacement})
	if err != nil {
		t.Fatal(err)
	}
	changeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", bytes.NewReader(changeBody))
	changeRequest.Header.Set("Content-Type", "application/json")
	changeRequest.AddCookie(cookie)
	changeResult := httptest.NewRecorder()
	f.server.ServeHTTP(changeResult, changeRequest)
	if changeResult.Code != http.StatusNoContent {
		t.Fatalf("password change status=%d body=%s", changeResult.Code, changeResult.Body.String())
	}
	if cleared := changeResult.Result().Cookies(); len(cleared) != 1 || cleared[0].MaxAge != -1 {
		t.Fatalf("password change did not clear session cookie: %#v", cleared)
	}

	staleRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	staleRequest.AddCookie(cookie)
	staleResult := httptest.NewRecorder()
	f.server.ServeHTTP(staleResult, staleRequest)
	if staleResult.Code != http.StatusUnauthorized {
		t.Fatalf("old session status=%d", staleResult.Code)
	}
	reloginBody, err := json.Marshal(map[string]string{"username": "admin", "password": replacement})
	if err != nil {
		t.Fatal(err)
	}
	reloginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(reloginBody))
	reloginRequest.Header.Set("Content-Type", "application/json")
	reloginResult := httptest.NewRecorder()
	f.server.ServeHTTP(reloginResult, reloginRequest)
	if reloginResult.Code != http.StatusOK || !strings.Contains(reloginResult.Body.String(), `"password_change_required":false`) {
		t.Fatalf("post-change login status=%d body=%s", reloginResult.Code, reloginResult.Body.String())
	}
	if strings.Contains(reloginResult.Body.String(), replacement) {
		t.Fatal("login response disclosed replacement password")
	}
}
