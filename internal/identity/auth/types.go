package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("already exists")
	ErrAuditUnavailable   = errors.New("security audit unavailable")
)

type User struct {
	ID           string
	Username     string
	DisplayName  string
	PasswordHash string
	Enabled      bool
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

type Identity struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

func (u User) Identity() Identity {
	return Identity{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, CreatedAt: u.CreatedAt}
}

type Session struct {
	ID          string
	UserID      string
	TokenDigest string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	SourceIP    string
	UserAgent   string
}

type SessionView struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s Session) View() SessionView {
	return SessionView{ID: s.ID, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt}
}

type UserStore interface {
	FindUserByUsername(context.Context, string) (User, error)
	FindUserByID(context.Context, string) (User, error)
	SetLastLogin(context.Context, string, time.Time) error
}

type SessionStore interface {
	CreateSession(context.Context, Session) error
	FindSessionByDigest(context.Context, string) (Session, error)
	RevokeSessionByDigest(context.Context, string, time.Time) error
	RevokeSessionsForUser(context.Context, string, time.Time) (int, error)
}

type LoginInput struct {
	Username  string
	Password  string
	SourceIP  string
	UserAgent string
}

type IssuedSession struct {
	Token    string
	Session  SessionView
	Identity Identity
}

type AuthenticatedSession struct {
	Session  SessionView
	Identity Identity
}
