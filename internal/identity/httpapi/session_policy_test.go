package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"control-center/internal/identity/auth"
)

func TestSessionPolicyEndpointReturnsEffectivePolicyAndAudits(t *testing.T) {
	fixture := newHTTPFixture(t)
	cookie := fixture.login(t, "viewer")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session-policy", nil)
	request.RemoteAddr = "192.0.2.55:443"
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("session policy status=%d body=%s", result.Code, result.Body.String())
	}
	var body struct {
		Policy auth.SessionSecurityPolicyView `json:"policy"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Policy.AbsoluteTTLSeconds != 3600 || body.Policy.IdleTimeoutSeconds != 3600 {
		t.Fatalf("policy=%#v, want effective 1h/1h fixture policy", body.Policy)
	}
	if !body.Policy.ActivityRefreshesIdleDeadline || body.Policy.ActivityExtendsAbsoluteExpiry {
		t.Fatalf("unexpected session policy semantics: %#v", body.Policy)
	}
	if cacheControl := result.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", cacheControl)
	}

	records := fixture.log.Records()
	last := records[len(records)-1]
	if last.Action != "auth.session_policy_read" || last.Outcome != "success" || last.ActorID != "viewer-1" || last.SourceIP != "192.0.2.55" {
		t.Fatalf("unexpected policy audit event: %#v", last)
	}
}

func TestSessionPolicyEndpointDoesNotBypassRequiredPasswordChange(t *testing.T) {
	fixture := newHTTPFixture(t)
	loginBody := bytes.NewBufferString(`{"username":"admin","password":"admin"}`)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	login.Header.Set("Content-Type", "application/json")
	loginResult := httptest.NewRecorder()
	fixture.server.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("bootstrap login status=%d body=%s", loginResult.Code, loginResult.Body.String())
	}
	cookies := loginResult.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bootstrap login cookies=%d, want 1", len(cookies))
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session-policy", nil)
	request.AddCookie(cookies[0])
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("session policy bypassed first-login boundary: status=%d body=%s", result.Code, result.Body.String())
	}
}
