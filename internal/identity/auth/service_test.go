package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	hash, err := hasher.Hash("synthetic test password long enough")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), User{ID: "user-1", Username: "Administrator", DisplayName: "Local Administrator", PasswordHash: hash, Enabled: true, CreatedAt: time.Now().UTC()}); err != nil {
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
	issued, err := service.Login(context.Background(), LoginInput{Username: "ADMINISTRATOR", Password: "synthetic test password long enough"})
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
	_, wrongPassword := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic incorrect value"})
	_, missingUser := service.Login(context.Background(), LoginInput{Username: "missing", Password: "synthetic incorrect value"})
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
	issued, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestRequiredPasswordChangeRevokesSessionsAndClearsRequirement(t *testing.T) {
	store := NewMemoryStore()
	log := audit.NewMemoryLog()
	hasher := security.NewPasswordHasher()
	bootstrapHash, err := hasher.HashBootstrapAdminPassword()
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-time.Hour)
	if err := store.CreateUser(context.Background(), User{
		ID: "bootstrap-admin", Username: "admin", DisplayName: "Administrator",
		PasswordHash: bootstrapHash, Enabled: true, PasswordChangeRequired: true,
		CreatedAt: createdAt, PasswordChangedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, store, log, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Login(context.Background(), LoginInput{Username: "admin", Password: "admin", SourceIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !issued.PasswordChangeRequired {
		t.Fatal("bootstrap login did not expose password change requirement")
	}
	authenticated, err := service.Authenticate(context.Background(), issued.Token)
	if err != nil || !authenticated.PasswordChangeRequired {
		t.Fatalf("bootstrap session requirement missing: %#v err=%v", authenticated, err)
	}
	if err := service.ChangePassword(context.Background(), ChangePasswordInput{UserID: "bootstrap-admin", CurrentPassword: "wrong", NewPassword: "a compliant replacement password", SourceIP: "127.0.0.1"}); !errors.Is(err, ErrInvalidCurrentPassword) {
		t.Fatalf("wrong current password error=%v", err)
	}
	if err := service.ChangePassword(context.Background(), ChangePasswordInput{UserID: "bootstrap-admin", CurrentPassword: "admin", NewPassword: "short", SourceIP: "127.0.0.1"}); !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("short replacement password error=%v", err)
	}
	const replacement = "a compliant replacement password"
	if err := service.ChangePassword(context.Background(), ChangePasswordInput{UserID: "bootstrap-admin", CurrentPassword: "admin", NewPassword: replacement, SourceIP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("pre-change session remained usable: %v", err)
	}
	if _, err := service.Login(context.Background(), LoginInput{Username: "admin", Password: "admin"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bootstrap password remained usable: %v", err)
	}
	relogin, err := service.Login(context.Background(), LoginInput{Username: "admin", Password: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if relogin.PasswordChangeRequired {
		t.Fatal("password change requirement was not cleared")
	}
	serialized, err := json.Marshal(log.Records())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), replacement) || strings.Contains(string(serialized), "wrong") || strings.Contains(string(serialized), "short") {
		t.Fatal("audit records disclosed a plaintext password")
	}
}

func TestAuthenticationRejectsSessionOlderThanPassword(t *testing.T) {
	service, store, _ := newTestService(t)
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	issued, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.FindUserByID(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	newHash, err := service.hasher.Hash("new synthetic test password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ChangePasswordAndRevokeSessions(context.Background(), user.ID, user.PasswordHash, newHash, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	staleToken, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	staleSession := Session{
		ID: "concurrent-session", UserID: user.ID, TokenDigest: digestToken(staleToken),
		CredentialVersion: user.PasswordChangedAt,
		CreatedAt:         now.Add(2 * time.Second), ExpiresAt: issued.Session.ExpiresAt,
	}
	if err := store.CreateSession(context.Background(), staleSession, user.PasswordHash); !errors.Is(err, ErrConflict) {
		t.Fatalf("session issued against replaced credentials: %v", err)
	}
	staleSession.ID = "artificially-stale-session"
	if err := store.CreateSession(context.Background(), staleSession, newHash); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), staleToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("stale session accepted after password change: %v", err)
	}
}

func TestRevokeAllSessionsRevokesEveryActiveSessionAndAudits(t *testing.T) {
	service, _, log := newTestService(t)
	first, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough", SourceIP: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough", SourceIP: "192.0.2.11"})
	if err != nil {
		t.Fatal(err)
	}

	count, err := service.RevokeAllSessions(context.Background(), RevokeAllSessionsInput{UserID: "user-1", SourceIP: "192.0.2.12"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("revoked sessions=%d, want 2", count)
	}
	for _, token := range []string{first.Token, second.Token} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("revoked session remained usable: %v", err)
		}
	}

	records := log.Records()
	if len(records) < 4 {
		t.Fatalf("audit records=%d, want login and revocation evidence", len(records))
	}
	requested := records[len(records)-2]
	succeeded := records[len(records)-1]
	if requested.Action != "auth.sessions_revoke_all" || requested.Outcome != "requested" || requested.ActorID != "user-1" || requested.SubjectID != "user-1" {
		t.Fatalf("unexpected requested audit event: %#v", requested)
	}
	if succeeded.Action != "auth.sessions_revoke_all" || succeeded.Outcome != "success" || succeeded.ActorID != "user-1" || succeeded.SubjectID != "user-1" {
		t.Fatalf("unexpected success audit event: %#v", succeeded)
	}
	if got, ok := succeeded.Details["revoked_sessions"].(int); !ok || got != 2 {
		t.Fatalf("revoked session count audit detail=%#v", succeeded.Details["revoked_sessions"])
	}
}

type rejectingAuditLog struct{}

func (rejectingAuditLog) Append(context.Context, audit.Event) error {
	return errors.New("audit unavailable")
}

func TestRevokeAllSessionsFailsClosedWhenAuditIsUnavailable(t *testing.T) {
	service, _, _ := newTestService(t)
	issued, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	service.audit = rejectingAuditLog{}

	count, err := service.RevokeAllSessions(context.Background(), RevokeAllSessionsInput{UserID: "user-1", SourceIP: "192.0.2.20"})
	if !errors.Is(err, ErrAuditUnavailable) || count != 0 {
		t.Fatalf("revoke result count=%d err=%v", count, err)
	}
	if _, err := service.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("session was revoked without pre-mutation audit evidence: %v", err)
	}
}
