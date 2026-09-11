package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"control-center/internal/identity/auth"
)

func TestSessionInventoryAndTargetedRevocationEndpoints(t *testing.T) {
	fixture := newHTTPFixture(t)
	first := fixture.login(t, "viewer")
	second := fixture.login(t, "viewer")

	list := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
	list.AddCookie(first)
	listResult := httptest.NewRecorder()
	fixture.server.ServeHTTP(listResult, list)
	if listResult.Code != http.StatusOK {
		t.Fatalf("list sessions status=%d body=%s", listResult.Code, listResult.Body.String())
	}
	var body struct {
		Sessions []auth.SessionSecurityView `json:"sessions"`
	}
	if err := json.Unmarshal(listResult.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("listed sessions=%d, want 2", len(body.Sessions))
	}
	var currentID, otherID string
	for _, session := range body.Sessions {
		if session.Current {
			currentID = session.ID
		} else {
			otherID = session.ID
		}
	}
	if currentID == "" || otherID == "" {
		t.Fatalf("current/other session markers missing: %#v", body.Sessions)
	}

	revokeOther := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/"+otherID, nil)
	revokeOther.AddCookie(first)
	revokeOtherResult := httptest.NewRecorder()
	fixture.server.ServeHTTP(revokeOtherResult, revokeOther)
	if revokeOtherResult.Code != http.StatusNoContent {
		t.Fatalf("revoke other status=%d body=%s", revokeOtherResult.Code, revokeOtherResult.Body.String())
	}
	if cookies := revokeOtherResult.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("revoking another session unexpectedly changed current cookie: %#v", cookies)
	}

	currentCheck := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	currentCheck.AddCookie(first)
	currentCheckResult := httptest.NewRecorder()
	fixture.server.ServeHTTP(currentCheckResult, currentCheck)
	if currentCheckResult.Code != http.StatusOK {
		t.Fatalf("current session status=%d body=%s", currentCheckResult.Code, currentCheckResult.Body.String())
	}
	otherCheck := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	otherCheck.AddCookie(second)
	otherCheckResult := httptest.NewRecorder()
	fixture.server.ServeHTTP(otherCheckResult, otherCheck)
	if otherCheckResult.Code != http.StatusUnauthorized {
		t.Fatalf("targeted session remained active: status=%d body=%s", otherCheckResult.Code, otherCheckResult.Body.String())
	}

	revokeCurrent := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/"+currentID, nil)
	revokeCurrent.AddCookie(first)
	revokeCurrentResult := httptest.NewRecorder()
	fixture.server.ServeHTTP(revokeCurrentResult, revokeCurrent)
	if revokeCurrentResult.Code != http.StatusNoContent {
		t.Fatalf("revoke current status=%d body=%s", revokeCurrentResult.Code, revokeCurrentResult.Body.String())
	}
	cleared := revokeCurrentResult.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge != -1 {
		t.Fatalf("current-session revocation did not clear cookie: %#v", cleared)
	}
}

func TestTargetedSessionRevocationReturnsNotFoundForMalformedIdentifier(t *testing.T) {
	fixture := newHTTPFixture(t)
	cookie := fixture.login(t, "viewer")
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/not-a-session", nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusNotFound {
		t.Fatalf("malformed session id status=%d body=%s", result.Code, result.Body.String())
	}
}
