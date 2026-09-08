package auth

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemoryStore is suitable for tests and single-process development. Production
// integration should implement the same interfaces with the 0002 SQL schema.
type MemoryStore struct {
	mu               sync.RWMutex
	usersByID        map[string]User
	userIDByName     map[string]string
	sessionsByDigest map[string]Session
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		usersByID:        make(map[string]User),
		userIDByName:     make(map[string]string),
		sessionsByDigest: make(map[string]Session),
	}
}

func (s *MemoryStore) CreateUser(_ context.Context, user User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	username := normalizeUsername(user.Username)
	if user.ID == "" || username == "" || user.PasswordHash == "" {
		return ErrConflict
	}
	if _, exists := s.usersByID[user.ID]; exists {
		return ErrConflict
	}
	if _, exists := s.userIDByName[username]; exists {
		return ErrConflict
	}
	user.Username = username
	user.CreatedAt = user.CreatedAt.UTC()
	s.usersByID[user.ID] = user
	s.userIDByName[username] = user.ID
	return nil
}

func (s *MemoryStore) FindUserByUsername(_ context.Context, username string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, exists := s.userIDByName[normalizeUsername(username)]
	if !exists {
		return User{}, ErrNotFound
	}
	return s.usersByID[id], nil
}

func (s *MemoryStore) FindUserByID(_ context.Context, id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, exists := s.usersByID[id]
	if !exists {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (s *MemoryStore) SetLastLogin(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, exists := s.usersByID[id]
	if !exists {
		return ErrNotFound
	}
	at = at.UTC()
	user.LastLoginAt = &at
	s.usersByID[id] = user
	return nil
}

func (s *MemoryStore) CreateSession(_ context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" || session.TokenDigest == "" || session.UserID == "" {
		return ErrConflict
	}
	if _, exists := s.sessionsByDigest[session.TokenDigest]; exists {
		return ErrConflict
	}
	s.sessionsByDigest[session.TokenDigest] = session
	return nil
}

func (s *MemoryStore) FindSessionByDigest(_ context.Context, digest string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, exists := s.sessionsByDigest[digest]
	if !exists {
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) RevokeSessionByDigest(_ context.Context, digest string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessionsByDigest[digest]
	if !exists {
		return ErrNotFound
	}
	if session.RevokedAt == nil {
		at = at.UTC()
		session.RevokedAt = &at
		s.sessionsByDigest[digest] = session
	}
	return nil
}

func (s *MemoryStore) RevokeSessionsForUser(_ context.Context, userID string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	at = at.UTC()
	for digest, session := range s.sessionsByDigest {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &at
			s.sessionsByDigest[digest] = session
			count++
		}
	}
	return count, nil
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
