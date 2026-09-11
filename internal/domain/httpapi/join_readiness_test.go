package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJoinValidationHandler(t *testing.T) {
	handler := JoinValidationHandler()
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{name: "windows samba", body: `{"platform":"windows","provider":"samba-ad-dc","domain_name":"example.test"}`, want: http.StatusOK},
		{name: "legacy samba", body: `{"platform":"windows","provider":"samba-ad","domain_name":"example.test"}`, want: http.StatusOK},
		{name: "windows freeipa", body: `{"platform":"windows","provider":"freeipa","domain_name":"example.test"}`, want: http.StatusUnprocessableEntity},
		{name: "unknown field", body: `{"platform":"linux","provider":"freeipa","domain_name":"example.test","command":"x"}`, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/domain/join/validate", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestReadinessHandler(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/domain/readiness/evaluate", strings.NewReader(`{"provider":"samba-ad-dc","dns_ready":true,"time_sync_ready":true,"storage_ready":true}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ReadinessHandler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Ready":true`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}
