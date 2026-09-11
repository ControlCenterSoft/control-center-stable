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

const (
	DefaultSessionTTL         = 8 * time.Hour
	DefaultSessionIdleTimeout = 2 * time.Hour
)

type ServiceOption func(*Service)

func WithSessionIdleTimeout(timeout time.Duration) ServiceOption {
	return func(service *Service) {
		service.sessionIdleTimeout = timeout
	}
}

type Service struct {
	users              UserStore
	sessions           SessionStore
	audit              audit.Logger
	hasher             security.PasswordHasher
	dummyHash          string
	sessionTTL         time.Duration
	sessionIdleTimeout time.Duration
	loginProtection    *LoginProtector
	now                func() time.Time
}

func NewService(users UserStore, sessions SessionStore, log audit.Logger, hasher security.PasswordHasher, sessionTTL time.Duration, options ...ServiceOption) (*Service, error) {
	if users == nil || sessions == nil || log == nil {
		return nil, errors.New("auth stores and audit logger are required")
	}
	if sessionTTL == 0 {
		sessionTTL = DefaultSessionTTL
	}
	if sessionTTL < time.Minute || sessionTTL > 7*24*time.Hour {
		return nil, errors.New("session TTL outside allowed range")
	}
	idleTimeout := DefaultSessionIdleTimeout
	if idleTimeout > sessionTTL {
		idleTimeout = sessionTTL
	}
	dummySecret, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	dummyHash, err := hasher.Hash(dummySecret)
	if err != nil {
		return nil, fmt.Errorf("prepare timing equalizer: %w", err)
	}
	service := &Service{
		users: users, sessions: sessions, audit: log, hasher: hasher, dummyHash: dummyHash,
		sessionTTL: sessionTTL, sessionIdleTimeout: idleTimeout,
		loginProtection: newLoginProtector(DefaultLoginProtectionPolicy()),
		now:             func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if service.sessionIdleTimeout < time.Minute || service.sessionIdleTimeout > service.sessionTTL {
		return nil, errors.New("session idle timeout outside allowed range")
	}
	return service, nil
}
func (s *Service) Login(ctx context.Context, input LoginInput) (IssuedSession, error) {
	username := normalizeUsername(input.Username)
	now := s.now().UTC()
	if s.loginProtection.Blocked(username, input.SourceIP, now) {
		_ = s.audit.Append(ctx, audit.Event{Action: "auth.login", Outcome: "denied", SourceIP: input.SourceIP, Details: map[string]any{"username": username, "reason": "invalid_credentials", "login_protection": "blocked"}})
		return IssuedSession{}, ErrInvalidCredentials
	}
	user, findErr := s.users.FindUserByUsername(ctx, username)
	hash := s.dummyHash
	if findErr == nil {
		hash = user.PasswordHash
	}
	valid, verifyErr := s.hasher.Verify(input.Password, hash)
	if findErr != nil || verifyErr != nil || !valid || !user.Enabled {
		blocked := s.loginProtection.RecordFailure(username, input.SourceIP, now)
		details := map[string]any{"username": username, "reason": "invalid_credentials"}
		if blocked {
			details["login_protection"] = "blocked"
		}
		_ = s.audit.Append(ctx, audit.Event{Action: "auth.login", Outcome: "denied", SourceIP: input.SourceIP, Details: details})
		return IssuedSession{}, ErrInvalidCredentials
	}
	s.loginProtection.RecordSuccess(username)
	token, err := randomToken(32)
	if err != nil {
		return IssuedSession{}, err
	}
	session := Session{
		ID: randomID(), UserID: user.ID, TokenDigest: digestToken(token), CredentialVersion: user.PasswordChangedAt,
		CreatedAt: now, LastActivityAt: now, ExpiresAt: now.Add(s.sessionTTL), SourceIP: input.SourceIP, UserAgent: truncate(input.UserAgent, 512),
	}
	if err := s.sessions.CreateSession(ctx, session, user.PasswordHash); err != nil {
		return IssuedSession{}, fmt.Errorf("create session: %w", err)
	}
	if err := s.audit.Append(ctx, audit.Event{Action: "auth.login", Outcome: "success", ActorID: user.ID, SourceIP: input.SourceIP, Details: map[string]any{"session_id": session.ID}}); err != nil {
		_ = s.sessions.RevokeSessionByDigest(context.Background(), session.TokenDigest, now)
		return IssuedSession{}, ErrAuditUnavailable
	}
	_ = s.users.SetLastLogin(ctx, user.ID, now)
	return IssuedSession{Token: token, Session: session.View(), Identity: user.Identity(), PasswordChangeRequired: user.PasswordChangeRequired}, nil
}
func (s *Service) Authenticate(ctx context.Context, token string) (AuthenticatedSession, error) {
	digest, ok := validatedTokenDigest(token)
	if !ok {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	now := s.now().UTC()
	session, err := s.sessions.FindSessionByDigest(ctx, digest)
	if err != nil || !session.activeAt(now, s.sessionIdleTimeout) {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	user, err := s.users.FindUserByID(ctx, session.UserID)
	if err != nil || !user.Enabled {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	if !session.CredentialVersion.Equal(user.PasswordChangedAt) {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	if err := s.sessions.TouchSessionByDigest(ctx, digest, now); err != nil {
		return AuthenticatedSession{}, ErrUnauthenticated
	}
	session.LastActivityAt = now
	return AuthenticatedSession{Session: session.View(), Identity: user.Identity(), PasswordChangeRequired: user.PasswordChangeRequired}, nil
}

func (s *Service) ChangePassword(ctx context.Context, input ChangePasswordInput) error {
	user, err := s.users.FindUserByID(ctx, input.UserID)
	if err != nil || !user.Enabled {
		return ErrUnauthenticated
	}
	valid, verifyErr := s.hasher.Verify(input.CurrentPassword, user.PasswordHash)
	if verifyErr != nil || !valid {
		_ = s.audit.Append(ctx, audit.Event{Action: "auth.password_change", Outcome: "denied", ActorID: user.ID, SourceIP: input.SourceIP, Details: map[string]any{"reason": "invalid_current_password"}})
		return ErrInvalidCurrentPassword
	}
	same, verifyErr := s.hasher.Verify(input.NewPassword, user.PasswordHash)
	if verifyErr != nil {
		return fmt.Errorf("verify password reuse: %w", verifyErr)
	}
	if same {
		_ = s.audit.Append(ctx, audit.Event{Action: "auth.password_change", Outcome: "denied", ActorID: user.ID, SourceIP: input.SourceIP, Details: map[string]any{"reason": "password_reuse"}})
		return ErrPasswordPolicy
	}
	newHash, err := s.hasher.Hash(input.NewPassword)
	if err != nil {
		_ = s.audit.Append(ctx, audit.Event{Action: "auth.password_change", Outcome: "denied", ActorID: user.ID, SourceIP: input.SourceIP, Details: map[string]any{"reason": "password_policy"}})
		return fmt.Errorf("%w: %v", ErrPasswordPolicy, err)
	}
	now := s.now().UTC()
	if err := s.users.ChangePasswordAndRevokeSessions(ctx, user.ID, user.PasswordHash, newHash, now); err != nil {
		if errors.Is(err, ErrConflict) {
			return ErrConflict
		}
		return fmt.Errorf("change password: %w", err)
	}
	if err := s.audit.Append(ctx, audit.Event{Action: "auth.password_change", Outcome: "success", ActorID: user.ID, SourceIP: input.SourceIP}); err != nil {
		return ErrAuditUnavailable
	}
	return nil
}

func (s *Service) ListSessions(ctx context.Context, input ListSessionsInput) ([]SessionSecurityView, error) {
	user, err := s.users.FindUserByID(ctx, input.UserID)
	if err != nil || !user.Enabled {
		return nil, ErrUnauthenticated
	}
	now := s.now().UTC()
	sessions, err := s.sessions.ListActiveSessionsForUser(ctx, user.ID, now)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	views := make([]SessionSecurityView, 0, len(sessions))
	for _, session := range sessions {
		if !session.activeAt(now, s.sessionIdleTimeout) || !session.CredentialVersion.Equal(user.PasswordChangedAt) {
			continue
		}
		views = append(views, session.SecurityView(input.CurrentSessionID, s.sessionIdleTimeout))
	}
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.sessions_list", Outcome: "success", ActorID: user.ID, SubjectID: user.ID,
		SourceIP: input.SourceIP, Details: map[string]any{"active_sessions": len(views)},
	}); err != nil {
		return nil, ErrAuditUnavailable
	}
	return views, nil
}

func (s *Service) RevokeSession(ctx context.Context, input RevokeSessionInput) (RevokeSessionResult, error) {
	user, err := s.users.FindUserByID(ctx, input.UserID)
	if err != nil || !user.Enabled {
		return RevokeSessionResult{}, ErrUnauthenticated
	}
	if !validSessionID(input.SessionID) {
		return RevokeSessionResult{}, ErrNotFound
	}
	now := s.now().UTC()
	target, err := s.sessions.FindSessionForUserByID(ctx, user.ID, input.SessionID)
	if err != nil || !target.activeAt(now, s.sessionIdleTimeout) || !target.CredentialVersion.Equal(user.PasswordChangedAt) {
		return RevokeSessionResult{}, ErrNotFound
	}
	current := target.ID == input.CurrentSessionID
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.session_revoke", Outcome: "requested", ActorID: user.ID, SubjectID: user.ID,
		SourceIP: input.SourceIP, Details: map[string]any{"session_id": target.ID, "current_session": current},
	}); err != nil {
		return RevokeSessionResult{}, ErrAuditUnavailable
	}
	if err := s.sessions.RevokeSessionForUserByID(ctx, user.ID, target.ID, now); err != nil {
		_ = s.audit.Append(ctx, audit.Event{
			Action: "auth.session_revoke", Outcome: "failed", ActorID: user.ID, SubjectID: user.ID,
			SourceIP: input.SourceIP, Details: map[string]any{"session_id": target.ID, "reason": "session_not_active"},
		})
		if errors.Is(err, ErrNotFound) {
			return RevokeSessionResult{}, ErrNotFound
		}
		return RevokeSessionResult{}, fmt.Errorf("revoke session: %w", err)
	}
	result := RevokeSessionResult{SessionID: target.ID, CurrentSessionRevoked: current}
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.session_revoke", Outcome: "success", ActorID: user.ID, SubjectID: user.ID,
		SourceIP: input.SourceIP, Details: map[string]any{"session_id": target.ID, "current_session": current},
	}); err != nil {
		return result, ErrAuditUnavailable
	}
	return result, nil
}

func (s *Service) RevokeAllSessions(ctx context.Context, input RevokeAllSessionsInput) (int, error) {
	user, err := s.users.FindUserByID(ctx, input.UserID)
	if err != nil || !user.Enabled {
		return 0, ErrUnauthenticated
	}
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.sessions_revoke_all", Outcome: "requested", ActorID: user.ID, SubjectID: user.ID,
		SourceIP: input.SourceIP,
	}); err != nil {
		return 0, ErrAuditUnavailable
	}
	now := s.now().UTC()
	count, err := s.sessions.RevokeSessionsForUser(ctx, user.ID, now)
	if err != nil {
		_ = s.audit.Append(ctx, audit.Event{
			Action: "auth.sessions_revoke_all", Outcome: "failed", ActorID: user.ID, SubjectID: user.ID,
			SourceIP: input.SourceIP, Details: map[string]any{"reason": "session_store_unavailable"},
		})
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	if err := s.audit.Append(ctx, audit.Event{
		Action: "auth.sessions_revoke_all", Outcome: "success", ActorID: user.ID, SubjectID: user.ID,
		SourceIP: input.SourceIP, Details: map[string]any{"revoked_sessions": count},
	}); err != nil {
		return count, ErrAuditUnavailable
	}
	return count, nil
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
	if err := s.audit.Append(ctx, audit.Event{Action: "auth.logout", Outcome: "success", ActorID: session.UserID, SourceIP: sourceIP, Details: map[string]any{"session_id": session.ID}}); err != nil {
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
func validSessionID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 36 {
		if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
			return false
		}
		value = strings.ReplaceAll(value, "-", "")
	}
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
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
