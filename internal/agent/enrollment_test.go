package agent

import (
	"errors"
	"testing"
)

func TestNormalizeEnrollment(t *testing.T) {
	request, err := NormalizeEnrollment(EnrollmentRequest{
		NodeID:   " node-001 ",
		Hostname: " server-01 ",
		Capabilities: []string{
			"PXE", "inventory", "pxe", " automation ",
		},
	})
	if err != nil {
		t.Fatalf("NormalizeEnrollment() error = %v", err)
	}
	if request.NodeID != "node-001" || request.Hostname != "server-01" {
		t.Fatalf("request = %#v", request)
	}
	want := []string{"automation", "inventory", "pxe"}
	if len(request.Capabilities) != len(want) {
		t.Fatalf("Capabilities = %#v", request.Capabilities)
	}
	for i := range want {
		if request.Capabilities[i] != want[i] {
			t.Fatalf("Capabilities[%d] = %q, want %q", i, request.Capabilities[i], want[i])
		}
	}
}

func TestNormalizeEnrollmentRequiresIdentity(t *testing.T) {
	_, err := NormalizeEnrollment(EnrollmentRequest{Hostname: "server"})
	if !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("error = %v, want ErrInvalidEnrollment", err)
	}
}
