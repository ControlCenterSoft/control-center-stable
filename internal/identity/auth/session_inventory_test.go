package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListSessionsReturnsOnlyCurrentUsersActiveSessions(t *testing.T) {
	service, store, log := newTestService(t)
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	first, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
		SourceIP: "192.0.2.10", UserAgent: "browser-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	second, err := service.Login(context.Background(), LoginInput{
		Username: "administrator", Password: "synthetic test password long enough",
		SourceIP: "192.0.2.11", UserAgent: "browser-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	hasher := service.hasher
	otherHash, err := hasher.Hash("another synthetic test password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), User{
		ID: "user-2", Username: "other", DisplayName: "Other User", PasswordHash: otherHash,
		Enabled: true, CreatedAt: now, PasswordChangedAt: time.Time{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(context.Background(), LoginInput{Username: "other", Password: "another synthetic test password"}); err != nil {
		t.Fatal(err)
	}

	views, err := service.ListSessions(context.Background(), ListSessionsInput{
		UserID: "user-1", CurrentSessionID: second.Session.ID, SourceIP: "192.0.2.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("sessions=%d, want 2", len(views))
	}
	if views[0].ID != second.Session.ID || !views[0].Current || views[0].SourceIP != "192.0.2.11" || views[0].UserAgent != "browser-b" {
		t.Fatalf("unexpected newest/current session: %#v", views[0])
	}
	if views[1].ID != first.Session.ID || views[1].Current || views[1].SourceIP != "192.0.2.10" || views[1].UserAgent != "browser-a" {
		t.Fatalf("unexpected older session: %#v", views[1])
	}

	records := log.Records()
	last := records[len(records)-1]
	if last.Action != "auth.sessions_list" || last.Outcome != "success" || last.ActorID != "user-1" || last.SubjectID != "user-1" {
		t.Fatalf("unexpected session inventory audit event: %#v", last)
	}
	if got, ok := last.Details["active_sessions"].(int); !ok || got != 2 {
		t.Fatalf("active session count=%#v", last.Details["active_sessions"])
	}
}

func TestRevokeSessionRevokesOnlyOwnedTargetSession(t *testing.T) {
	service, _, log := newTestService(t)
	current, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.RevokeSession(context.Background(), RevokeSessionInput{
		UserID: "user-1", SessionID: target.Session.ID, CurrentSessionID: current.Session.ID, SourceIP: "192.0.2.30",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != target.Session.ID || result.CurrentSessionRevoked {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := service.Authenticate(context.Background(), current.Token); err != nil {
		t.Fatalf("current session was revoked: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), target.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("target session remained active: %v", err)
	}

	records := log.Records()
	requested := records[len(records)-2]
	succeeded := records[len(records)-1]
	if requested.Action != "auth.session_revoke" || requested.Outcome != "requested" || succeeded.Action != "auth.session_revoke" || succeeded.Outcome != "success" {
		t.Fatalf("unexpected revocation audit events: requested=%#v success=%#v", requested, succeeded)
	}
}

func TestRevokeSessionRejectsCrossUserAndMalformedIdentifiers(t *testing.T) {
	service, store, _ := newTestService(t)
	current, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}

	otherHash, err := service.hasher.Hash("another synthetic test password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), User{
		ID: "user-2", Username: "other", DisplayName: "Other User", PasswordHash: otherHash,
		Enabled: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	other, err := service.Login(context.Background(), LoginInput{Username: "other", Password: "another synthetic test password"})
	if err != nil {
		t.Fatal(err)
	}

	for _, sessionID := range []string{"not-a-session", other.Session.ID} {
		_, err := service.RevokeSession(context.Background(), RevokeSessionInput{
			UserID: "user-1", SessionID: sessionID, CurrentSessionID: current.Session.ID,
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("session id %q error=%v, want not found", sessionID, err)
		}
	}
	if _, err := service.Authenticate(context.Background(), other.Token); err != nil {
		t.Fatalf("cross-user target was changed: %v", err)
	}
}

func TestRevokeSessionFailsClosedBeforeMutationWhenAuditUnavailable(t *testing.T) {
	service, _, _ := newTestService(t)
	current, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Login(context.Background(), LoginInput{Username: "administrator", Password: "synthetic test password long enough"})
	if err != nil {
		t.Fatal(err)
	}
	service.audit = rejectingAuditLog{}

	_, err = service.RevokeSession(context.Background(), RevokeSessionInput{
		UserID: "user-1", SessionID: target.Session.ID, CurrentSessionID: current.Session.ID,
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("revoke error=%v, want audit unavailable", err)
	}
	if _, err := service.Authenticate(context.Background(), target.Token); err != nil {
		t.Fatalf("session changed without pre-mutation audit evidence: %v", err)
	}
}
