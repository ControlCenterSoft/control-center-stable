package config_test

import (
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/config"
)

func TestRevisionIsImmutable(t *testing.T) {
	source := []byte(`{"dns":"10.0.0.1"}`)
	rev, err := config.NewRevision("rev-1", 1, time.Unix(1, 0), source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'x'
	first := rev.Content()
	first[0] = 'y'
	if got := string(rev.Content()); got != `{"dns":"10.0.0.1"}` {
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

func TestRevisionCanonicalizesJSONForStableStorageIdentity(t *testing.T) {
	first, err := config.NewRevision("rev-a", 1, time.Unix(1, 0), []byte(`{"z": 2, "a": 1.0}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := config.NewRevision("rev-b", 2, time.Unix(2, 0), []byte(`{"a":1.0,"z":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(first.Content()), `{"a":1.0,"z":2}`; got != want {
		t.Fatalf("canonical content=%q want=%q", got, want)
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("equivalent JSON digests differ: %q != %q", first.Digest(), second.Digest())
	}

	plain, err := config.NewRevision("rev-plain", 3, time.Unix(3, 0), []byte(" config \n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(plain.Content()); got != " config \n" {
		t.Fatalf("non-JSON content changed: %q", got)
	}
}
