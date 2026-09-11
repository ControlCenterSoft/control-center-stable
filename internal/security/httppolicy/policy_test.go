package httppolicy

import "testing"

func TestHeadersApplyTLSOnlyHSTS(t *testing.T) {
	plain := Headers(Policy{})
	if _, ok := plain["Strict-Transport-Security"]; ok {
		t.Fatal("HSTS must not be emitted for a non-TLS listener")
	}

	tlsHeaders := Headers(Policy{TLS: true})
	if tlsHeaders["Strict-Transport-Security"] == "" {
		t.Fatal("HSTS is required for TLS")
	}
	if tlsHeaders["X-Content-Type-Options"] != "nosniff" {
		t.Fatal("nosniff policy is required")
	}
}
