package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRevokeAllSessionsEndpointRevokesEverySessionAndClearsCookie(t *testing.T) {
	fixture := newHTTPFixture(t)
	first := fixture.login(t, "viewer")
	second := fixture.login(t, "viewer")

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions/revoke-all", nil)
	request.AddCookie(first)
	result := httptest.NewRecorder()
	fixture.server.ServeHTTP(result, request)
	if result.Code != http.StatusNoContent {
		t.Fatalf("revoke-all status=%d body=%s", result.Code, result.Body.String())
	}
	cleared := result.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge != -1 {
		t.Fatalf("revoke-all did not clear session cookie: %#v", cleared)
	}

	for _, cookie := range []*http.Cookie{first, second} {
		after := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		after.AddCookie(cookie)
		afterResult := httptest.NewRecorder()
		fixture.server.ServeHTTP(afterResult, after)
		if afterResult.Code != http.StatusUnauthorized {
			t.Fatalf("revoked session status=%d body=%s", afterResult.Code, afterResult.Body.String())
		}
	}

	records := fixture.log.Records()
	if len(records) < 4 {
		t.Fatalf("audit records=%d, want login and revocation evidence", len(records))
	}
	requested := records[len(records)-2]
	succeeded := records[len(records)-1]
	if requested.Action != "auth.sessions_revoke_all" || requested.Outcome != "requested" {
		t.Fatalf("unexpected revocation request event: %#v", requested)
	}
	if succeeded.Action != "auth.sessions_revoke_all" || succeeded.Outcome != "success" {
		t.Fatalf("unexpected revocation success event: %#v", succeeded)
	}
	if got, ok := succeeded.Details["revoked_sessions"].(int); !ok || got != 2 {
		t.Fatalf("revoked session count audit detail=%#v", succeeded.Details["revoked_sessions"])
	}
}
