package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebAuthenticationBoundaryRedirectsBrowserAndPreservesAPI401(t *testing.T) {
	f := newHTTPFixture(t)
	for _, path := range []string{"/overview", "/password/change"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		result := httptest.NewRecorder()
		f.server.ServeHTTP(result, request)
		if result.Code != http.StatusSeeOther {
			t.Fatalf("%s status=%d body=%s", path, result.Code, result.Body.String())
		}
		if location := result.Header().Get("Location"); location != "/login" {
			t.Fatalf("%s location=%q", path, location)
		}
		if strings.Contains(result.Body.String(), "authentication_required") {
			t.Fatalf("%s exposed API authentication envelope to browser", path)
		}
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/system/overview", nil)
	apiResult := httptest.NewRecorder()
	f.server.ServeHTTP(apiResult, apiRequest)
	if apiResult.Code != http.StatusUnauthorized || !strings.Contains(apiResult.Body.String(), "authentication_required") {
		t.Fatalf("API auth contract changed: status=%d body=%s", apiResult.Code, apiResult.Body.String())
	}
}

func TestWebAuthenticationBoundaryHandlesValidAndFirstLoginSessions(t *testing.T) {
	f := newHTTPFixture(t)
	viewerCookie := f.login(t, "viewer")
	viewerRequest := httptest.NewRequest(http.MethodGet, "/overview", nil)
	viewerRequest.AddCookie(viewerCookie)
	viewerResult := httptest.NewRecorder()
	f.server.ServeHTTP(viewerResult, viewerRequest)
	if viewerResult.Code != http.StatusOK {
		t.Fatalf("viewer overview status=%d body=%s", viewerResult.Code, viewerResult.Body.String())
	}

	loginBody := strings.NewReader(`{"username":"admin","password":"admin"}`)
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResult := httptest.NewRecorder()
	f.server.ServeHTTP(loginResult, loginRequest)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("admin login status=%d body=%s", loginResult.Code, loginResult.Body.String())
	}
	cookies := loginResult.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("admin login cookies=%d", len(cookies))
	}
	adminOverview := httptest.NewRequest(http.MethodGet, "/overview", nil)
	adminOverview.AddCookie(cookies[0])
	adminOverviewResult := httptest.NewRecorder()
	f.server.ServeHTTP(adminOverviewResult, adminOverview)
	if adminOverviewResult.Code != http.StatusSeeOther || adminOverviewResult.Header().Get("Location") != "/password/change" {
		t.Fatalf("first-login overview status=%d location=%q body=%s", adminOverviewResult.Code, adminOverviewResult.Header().Get("Location"), adminOverviewResult.Body.String())
	}
}
