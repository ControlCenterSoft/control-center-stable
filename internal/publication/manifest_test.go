package publication

import (
	"errors"
	"testing"
)

func TestCanonicalID(t *testing.T) {
	id, err := CanonicalID(Manifest{Product: "control-center", Version: "0.4.0", Channel: "candidate", Commit: "abcdef1234567"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "control-center@0.4.0:candidate" {
		t.Fatalf("got %q", id)
	}
}

func TestValidateRejectsUnknownChannel(t *testing.T) {
	err := Validate(Manifest{Product: "control-center", Version: "0.4.0", Channel: "latest", Commit: "abcdef1"})
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("got %v, want ErrInvalidManifest", err)
	}
}
