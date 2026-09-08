package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/security"
)

const DefaultSessionTTL = 8 * time.Hour

type Service struct {
	users      UserStore
	sessions   SessionStore
	audit      audit.Logger
	hasher     security.PasswordHasher
	dummyHash  string
	sessionTTL time.Duration
	now        func() time.Time
}

func NewService(users UserStore, sessions SessionStore, log audit.Logger, hasher security.PasswordHasher, sessionTTL time.Duration) (*Service, error) {
	if users == nil || sessions == nil || log == nil {
		return nil, errors.New("auth stores and audit logger are required")
	}
	if sessionTTL == 0 {
		sessionTTL = DefaultSessionTTL
	}
	if sessionTTL < time.Minute || sessionTTL > 7*24*time.Hour {
		return nil, errors.New("session TTL outside allowed range")
	}
	dummySecret, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	dummyHash, err := hasher.Hash(dummySecret)
	if err != nil {
		return nil, fmt.Errorf("prepare timing equalizer: %w", err)
	}
	return &Service{
		users: users, sessions: sessions, audit: log, hasher: hasher,
		dummyHash: dummyHash, sessionTTL: sessionTTL, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) Login(ctx context.Context, input LoginInput) (IssuedSession, error) {
	username := normalizeUsername(input.Username)
	user, findErr := s.users.FindUserByUsername(ctx, username)
	hash := s.dummyHash
	if findErr == nil {
		hash = user.PasswordHash
	}
	valid, verifyErr := s.hasher.Verify(input.Password, hash)
	if findErr != nil || verifyErr != nil || !valid || !user.Enabled {
		_ = s.audit.Append(ctx, audit.Event{
			Action: "auth.login", Outcome: "denied", SourceIP: input.SourceIP,
			Details: map[string]any{"username": username, "reason": "invalid_credentials"},
		})
		return IssuedSession{}, ErrInvalidCredentials
	}

	now := s.now().UTC()
	token, err := randomToken(32)
	if err != nil {
		return IssuedSession{}, err
	}
	session := Session{
		ID: randomID(), UserID: user.ID, TokenDigest: digestToken(token),
		CreatedAt: now, ExpiresAt: now.Add(s.sessionTTL), SourceIP: input.SourceIP,
		UserAgent: truncate(input.UserAgent, 512),
	}
	if err := s.sessions.CreateSession(ctx, session); err != nil {
		return IssuedSession{}, fmt.Errorf("create session: %w", err)
	}
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.login", Outcome: "success", ActorID: user.ID, SourceIP: input.SourceIP,
		Details: map[string]any{"session_id": session.ID},
	}); err != nil {
		_ = s.sessions.RevokeSessionByDigest(context.Background(), session.TokenDigest, now)
		return IssuedSession{}, ErrAuditUnavailable
	}
	_ = s.users.SetLastLogin(ctx, user.ID, now)
	return IssuedSession{Token: token, Session: session.View(), Identity: user.Identity()}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (AuthenticatedSession, error) {
	digest, ok := validatedTokenDigest(token)
	if !ok {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	session, err := s.sessions.FindSessionByDigest(ctx, digest)
	if err != nil || session.RevokedAt != nil || !s.now().Before(session.ExpiresAt) {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	user, err := s.users.FindUserByID(ctx, session.UserID)
	if err != nil || !user.Enabled {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	return AuthenticatedSession{Session: session.View(), Identity: user.Identity()}, nil
}

func (s *Service) Logout(ctx context.Context, token, sourceIP string) error {
	digest, ok := validatedTokenDigest(token)
	if !ok {
		return nil
	}
	session, err := s.sessions.FindSessionByDigest(ctx, digest)
	if err != nil {
		return nil
	}
	now := s.now().UTC()
	if err := s.sessions.RevokeSessionByDigest(ctx, digest, now); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.logout", Outcome: "success", ActorID: session.UserID, SourceIP: sourceIP,
		Details: map[string]any{"session_id": session.ID},
	}); err != nil {
		return ErrAuditUnavailable
	}
	return nil
}

func digestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validatedTokenDigest(token string) (string, bool) {
	token = strings.TrimSpace(token)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return "", false
	}
	return digestToken(token), true
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secure random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("operating system random source unavailable")
	}
	return hex.EncodeToString(b)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
