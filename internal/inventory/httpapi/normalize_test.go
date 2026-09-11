package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeHandlerCanonicalizesDevice(t *testing.T) {
	body := `{"hostname":" node-01 ","platform":"linux","machine_id":"abc","addresses":["10.0.0.2","10.0.0.2"," 10.0.0.1 "],"tags":["Prod","prod"," Linux "]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/normalize", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	NormalizeHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, want := range []string{`"hostname":"node-01"`, `"addresses":["10.0.0.1","10.0.0.2"]`, `"tags":["Linux","Prod"]`} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("body=%s missing %s", res.Body.String(), want)
		}
	}
}

func TestNormalizeHandlerRejectsMissingIdentity(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/normalize", strings.NewReader(`{"hostname":"node-01","platform":"linux"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	NormalizeHandler().ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestNormalizeHandlerRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/normalize", strings.NewReader(`{"hostname":"node-01","platform":"linux","machine_id":"abc","extra":1}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	NormalizeHandler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
