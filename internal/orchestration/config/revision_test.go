package config_test

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/config"
)

func TestRevisionIsImmutable(t *testing.T) {
	source := []byte(`{"dns":"192.0.2.1"}`)
	rev, err := config.NewRevision("rev-1", 1, time.Unix(1, 0), source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'x'
	first := rev.Content()
	first[0] = 'y'
	if got := string(rev.Content()); got != `{"dns":"192.0.2.1"}` {
		t.Fatalf("revision mutated: %q", got)
	}
}

func TestRevisionPrecondition(t *testing.T) {
	rev, err := config.NewRevision("rev-2", 2, time.Unix(2, 0), []byte("config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := (config.Precondition{ExpectedRevisionID: rev.ID(), ExpectedDigest: rev.Digest()}).ValidateAgainst(rev); err != nil {
		t.Fatalf("valid precondition failed: %v", err)
	}
	err = (config.Precondition{ExpectedRevisionID: "stale"}).ValidateAgainst(rev)
	if !errors.Is(err, config.ErrPreconditionFailed) {
		t.Fatalf("expected precondition failure, got %v", err)
	}
}
