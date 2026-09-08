package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/security"
)

func newTestService(t *testing.T) (*Service, *MemoryStore, *audit.MemoryLog) {
	t.Helper()
	store := NewMemoryStore()
	log := audit.NewMemoryLog()
	hasher := security.NewPasswordHasher()
	hash, err := hasher.Hash("test password is long enough")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), User{
		ID: "user-1", Username: "Administrator", DisplayName: "Local Administrator",
		PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, store, log, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, log
}

func TestSessionLifecycle(t *testing.T) {
	service, _, _ := newTestService(t)
	issued, err := service.Login(context.Background(), LoginInput{Username: "ADMINISTRATOR", Password: "test password is long enough"})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.Identity.ID != "user-1" {
		t.Fatal("login did not issue a session")
	}
	if _, err := service.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	if err := service.Logout(context.Background(), issued.Token, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session accepted: %v", err)
	}
}

func TestLoginFailureIsGeneric(t *testing.T) {
	service, _, log := newTestService(t)
	_, wrongPassword := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "this value is not correct"})
	_, missingUser := service.Login(context.Background(), LoginInput{Username: "missing", Password: "this value is not correct"})
	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(missingUser, ErrInvalidCredentials) || wrongPassword.Error() != missingUser.Error() {
		t.Fatal("login errors enable account enumeration")
	}
	if len(log.Records()) != 2 {
		t.Fatal("failed logins were not audited")
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	service, _, _ := newTestService(t)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	issued, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "test password is long enough"})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session accepted: %v", err)
	}
}
