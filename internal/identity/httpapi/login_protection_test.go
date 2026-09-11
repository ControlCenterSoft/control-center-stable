package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginAbuseProtectionKeepsGenericHTTPContract(t *testing.T) {
	fixture := newHTTPFixture(t)

	login := func(username, password string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{"username": username, "password": password})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.44:43120"
		result := httptest.NewRecorder()
		fixture.server.ServeHTTP(result, req)
		return result
	}

	for i := 0; i < 8; i++ {
		result := login("viewer", "wrong password")
		if result.Code != http.StatusUnauthorized {
			t.Fatalf("failed login %d status=%d body=%s", i+1, result.Code, result.Body.String())
		}
	}

	blocked := login("viewer", "a secure test password")
	if blocked.Code != http.StatusUnauthorized {
		t.Fatalf("blocked valid credential status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(blocked.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "invalid_credentials" {
		t.Fatalf("blocked response leaks protection state: code=%q body=%s", envelope.Error.Code, blocked.Body.String())
	}
}
