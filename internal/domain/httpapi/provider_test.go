package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderHandlerSelectsSambaForWindowsRequirements(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domain/provider/resolve", strings.NewReader(`{"preferred":"auto","requirements":{"WindowsDomainJoin":true,"GroupPolicy":false}}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	ProviderHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"provider":"samba-ad-dc"`) {
		t.Fatalf("body=%s", res.Body.String())
	}
}

func TestProviderHandlerRejectsIncompatibleFreeIPA(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domain/provider/resolve", strings.NewReader(`{"preferred":"freeipa","requirements":{"WindowsDomainJoin":true}}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	ProviderHandler().ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestProviderHandlerRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domain/provider/resolve", strings.NewReader(`{"preferred":"auto","unexpected":true}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	ProviderHandler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestProviderHandlerRejectsMultipleJSONValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domain/provider/resolve", strings.NewReader(`{"preferred":"auto"} {}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	ProviderHandler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
