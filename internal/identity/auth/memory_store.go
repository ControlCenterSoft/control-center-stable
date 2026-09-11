package auth

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu               sync.RWMutex
	usersByID        map[string]User
	userIDByName     map[string]string
	sessionsByDigest map[string]Session
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{usersByID: make(map[string]User), userIDByName: make(map[string]string), sessionsByDigest: make(map[string]Session)}
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
func (s *MemoryStore) ChangePasswordAndRevokeSessions(_ context.Context, id, expectedHash, newHash string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, exists := s.usersByID[id]
	if !exists {
		return ErrNotFound
	}
	if user.PasswordHash != expectedHash {
		return ErrConflict
	}
	at = at.UTC()
	user.PasswordHash = newHash
	user.PasswordChangeRequired = false
	user.PasswordChangedAt = at
	s.usersByID[id] = user
	for digest, session := range s.sessionsByDigest {
		if session.UserID == id && session.RevokedAt == nil {
			session.RevokedAt = &at
			s.sessionsByDigest[digest] = session
		}
	}
	return nil
}
func (s *MemoryStore) CreateSession(_ context.Context, session Session, expectedPasswordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" || session.TokenDigest == "" || session.UserID == "" {
		return ErrConflict
	}
	user, exists := s.usersByID[session.UserID]
	if !exists || user.PasswordHash != expectedPasswordHash {
		return ErrConflict
	}
	if _, exists := s.sessionsByDigest[session.TokenDigest]; exists {
		return ErrConflict
	}
	session.CreatedAt = session.CreatedAt.UTC()
	session.ExpiresAt = session.ExpiresAt.UTC()
	if session.LastActivityAt.IsZero() {
		session.LastActivityAt = session.CreatedAt
	} else {
		session.LastActivityAt = session.LastActivityAt.UTC()
	}
	if session.LastActivityAt.Before(session.CreatedAt) || session.LastActivityAt.After(session.ExpiresAt) {
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
func (s *MemoryStore) FindSessionForUserByID(_ context.Context, userID, sessionID string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.sessionsByDigest {
		if session.UserID == userID && session.ID == sessionID {
			return session, nil
		}
	}
	return Session{}, ErrNotFound
}
func (s *MemoryStore) ListActiveSessionsForUser(_ context.Context, userID string, now time.Time) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now = now.UTC()
	result := make([]Session, 0)
	for _, session := range s.sessionsByDigest {
		if session.UserID != userID || session.RevokedAt != nil || !now.Before(session.ExpiresAt) {
			continue
		}
		result = append(result, session)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}
func (s *MemoryStore) TouchSessionByDigest(_ context.Context, digest string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessionsByDigest[digest]
	if !exists || session.RevokedAt != nil {
		return ErrNotFound
	}
	at = at.UTC()
	if !at.Before(session.ExpiresAt) {
		return ErrNotFound
	}
	if at.After(session.activityAt()) {
		session.LastActivityAt = at
		s.sessionsByDigest[digest] = session
	}
	return nil
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
func (s *MemoryStore) RevokeSessionForUserByID(_ context.Context, userID, sessionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for digest, session := range s.sessionsByDigest {
		if session.UserID != userID || session.ID != sessionID || session.RevokedAt != nil || !at.UTC().Before(session.ExpiresAt) {
			continue
		}
		revokedAt := at.UTC()
		session.RevokedAt = &revokedAt
		s.sessionsByDigest[digest] = session
		return nil
	}
	return ErrNotFound
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
func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
