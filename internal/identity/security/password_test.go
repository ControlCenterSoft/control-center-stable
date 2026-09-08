package security

import (
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h := NewPasswordHasher()
	encoded, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "correct horse") {
		t.Fatal("encoded hash contains plaintext")
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected password hash format: %s", encoded)
	}
	ok, err := h.Verify("correct horse battery staple", encoded)
	if err != nil || !ok {
		t.Fatalf("valid password rejected: ok=%v err=%v", ok, err)
	}
	ok, err = h.Verify("incorrect password value", encoded)
	if err != nil || ok {
		t.Fatalf("invalid password accepted: ok=%v err=%v", ok, err)
	}
}

func TestPasswordPolicyAndMalformedHash(t *testing.T) {
	h := NewPasswordHasher()
	if _, err := h.Hash("short"); err == nil {
		t.Fatal("short password accepted")
	}
	hostile := "$argon2id$v=19$m=4294967295,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"
	if ok, err := h.Verify("any password value", hostile); err == nil || ok {
		t.Fatalf("hostile memory parameter accepted: ok=%v err=%v", ok, err)
	}
}
