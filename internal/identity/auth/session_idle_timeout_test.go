package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/security"
)

func TestSessionIdleTimeoutRejectsInactiveSession(t *testing.T) {
	service, store, _ := newTestService(t)
	service.sessionIdleTimeout = 15 * time.Minute
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	issued, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.FindSessionByDigest(context.Background(), digestToken(issued.Token))
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastActivityAt.Equal(now) {
		t.Fatalf("last activity=%s, want login time %s", persisted.LastActivityAt, now)
	}

	now = now.Add(15 * time.Minute)
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("idle-expired session accepted: %v", err)
	}
	persisted, err = store.FindSessionByDigest(context.Background(), digestToken(issued.Token))
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastActivityAt.Equal(time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("rejected authentication unexpectedly refreshed activity: %s", persisted.LastActivityAt)
	}
}

func TestAuthenticationRefreshesSessionActivity(t *testing.T) {
	service, store, _ := newTestService(t)
	service.sessionIdleTimeout = 15 * time.Minute
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	issued, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if _, err := service.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("active session rejected: %v", err)
	}
	persisted, err := store.FindSessionByDigest(context.Background(), digestToken(issued.Token))
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastActivityAt.Equal(now) {
		t.Fatalf("last activity=%s, want %s", persisted.LastActivityAt, now)
	}

	now = now.Add(14 * time.Minute)
	if _, err := service.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatalf("session was not extended by recent activity: %v", err)
	}
}

func TestSessionActivityTouchIsMonotonic(t *testing.T) {
	service, store, _ := newTestService(t)
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	issued, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := digestToken(issued.Token)
	newer := now.Add(10 * time.Minute)
	older := now.Add(5 * time.Minute)
	if err := store.TouchSessionByDigest(context.Background(), digest, newer); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchSessionByDigest(context.Background(), digest, older); err != nil {
		t.Fatalf("stale concurrent activity should be an idempotent no-op: %v", err)
	}
	persisted, err := store.FindSessionByDigest(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastActivityAt.Equal(newer) {
		t.Fatalf("stale activity regressed timestamp to %s, want %s", persisted.LastActivityAt, newer)
	}
}

func TestSessionInventoryOmitsIdleExpiredSessionsAndReportsIdleDeadline(t *testing.T) {
	service, _, _ := newTestService(t)
	service.sessionIdleTimeout = 15 * time.Minute
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	first, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	second, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)

	views, err := service.ListSessions(context.Background(), ListSessionsInput{
		UserID: "user-1", CurrentSessionID: second.Session.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != second.Session.ID {
		t.Fatalf("idle-expired session remained in inventory: %#v", views)
	}
	if !views[0].LastActivityAt.Equal(time.Date(2026, 9, 10, 1, 10, 0, 0, time.UTC)) {
		t.Fatalf("last activity=%s", views[0].LastActivityAt)
	}
	wantIdleExpiry := time.Date(2026, 9, 10, 1, 25, 0, 0, time.UTC)
	if !views[0].IdleExpiresAt.Equal(wantIdleExpiry) {
		t.Fatalf("idle expiry=%s, want %s", views[0].IdleExpiresAt, wantIdleExpiry)
	}
	if !views[0].Current || first.Session.ID == second.Session.ID {
		t.Fatalf("unexpected current-session metadata: %#v", views[0])
	}
}

func TestSessionIdleTimeoutOptionIsBoundedByAbsoluteTTL(t *testing.T) {
	store := NewMemoryStore()
	hasher := security.NewPasswordHasher()
	log := audit.NewMemoryLog()
	hash, err := hasher.Hash("synthetic test password long enough")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), User{
		ID: "user-1", Username: "administrator", PasswordHash: hash, Enabled: true,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(store, store, log, hasher, time.Hour, WithSessionIdleTimeout(2*time.Hour)); err == nil {
		t.Fatal("idle timeout longer than absolute session TTL was accepted")
	}
	if _, err := NewService(store, store, log, hasher, time.Hour, WithSessionIdleTimeout(30*time.Second)); err == nil {
		t.Fatal("sub-minute idle timeout was accepted")
	}
}
