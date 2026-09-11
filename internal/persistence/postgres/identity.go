package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"control-center/internal/identity/auth"
)

type IdentityStore struct{ db *sql.DB }

func NewIdentityStore(db *sql.DB) (*IdentityStore, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &IdentityStore{db: db}, nil
}
func (s *IdentityStore) FindUserByUsername(ctx context.Context, username string) (auth.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id::text, username, display_name, password_hash, enabled, password_change_required, created_at, password_changed_at, last_login_at FROM cc_local_users WHERE lower(username) = lower($1)`, strings.TrimSpace(username)))
}
func (s *IdentityStore) FindUserByID(ctx context.Context, id string) (auth.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id::text, username, display_name, password_hash, enabled, password_change_required, created_at, password_changed_at, last_login_at FROM cc_local_users WHERE id = $1::uuid`, id))
}
func scanUser(row *sql.Row) (auth.User, error) {
	var user auth.User
	var lastLogin sql.NullTime
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.PasswordHash, &user.Enabled, &user.PasswordChangeRequired, &user.CreatedAt, &user.PasswordChangedAt, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.User{}, auth.ErrNotFound
		}
		return auth.User{}, err
	}
	if lastLogin.Valid {
		value := lastLogin.Time.UTC()
		user.LastLoginAt = &value
	}
	return user, nil
}
func (s *IdentityStore) ChangePasswordAndRevokeSessions(ctx context.Context, id, expectedHash, newHash string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE cc_local_users SET password_hash=$3,password_change_required=false,password_changed_at=$4,updated_at=$4 WHERE id=$1::uuid AND password_hash=$2`, id, expectedHash, newHash, at.UTC())
	if err := requireAffected(result, err, auth.ErrConflict); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cc_auth_sessions SET revoked_at=$2 WHERE user_id=$1::uuid AND revoked_at IS NULL`, id, at.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *IdentityStore) SetLastLogin(ctx context.Context, id string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE cc_local_users SET last_login_at=$2, updated_at=$2 WHERE id=$1::uuid`, id, at.UTC())
	return requireAffected(result, err, auth.ErrNotFound)
}
func (s *IdentityStore) CreateSession(ctx context.Context, session auth.Session, expectedPasswordHash string) error {
	lastActivity := session.LastActivityAt
	if lastActivity.IsZero() {
		lastActivity = session.CreatedAt
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO cc_auth_sessions (id,user_id,token_digest,credential_version,created_at,last_activity_at,expires_at,source_ip,user_agent) SELECT $1::uuid,id,$3,$4,$5,$6,$7,NULLIF($8,'')::inet,$9 FROM cc_local_users WHERE id=$2::uuid AND password_hash=$10`, session.ID, session.UserID, session.TokenDigest, session.CredentialVersion.UTC(), session.CreatedAt.UTC(), lastActivity.UTC(), session.ExpiresAt.UTC(), session.SourceIP, session.UserAgent, expectedPasswordHash)
	if isUniqueViolation(err) {
		return auth.ErrConflict
	}
	return requireAffected(result, err, auth.ErrConflict)
}
func (s *IdentityStore) FindSessionByDigest(ctx context.Context, digest string) (auth.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT id::text,user_id::text,token_digest,credential_version,created_at,last_activity_at,expires_at,revoked_at,host(source_ip),user_agent FROM cc_auth_sessions WHERE token_digest=$1`, digest))
}
func (s *IdentityStore) FindSessionForUserByID(ctx context.Context, userID, sessionID string) (auth.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT id::text,user_id::text,token_digest,credential_version,created_at,last_activity_at,expires_at,revoked_at,host(source_ip),user_agent FROM cc_auth_sessions WHERE user_id=$1::uuid AND id=$2::uuid`, userID, sessionID))
}
func scanSession(row *sql.Row) (auth.Session, error) {
	var session auth.Session
	var revoked sql.NullTime
	var sourceIP sql.NullString
	err := row.Scan(&session.ID, &session.UserID, &session.TokenDigest, &session.CredentialVersion, &session.CreatedAt, &session.LastActivityAt, &session.ExpiresAt, &revoked, &sourceIP, &session.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Session{}, err
	}
	if revoked.Valid {
		value := revoked.Time.UTC()
		session.RevokedAt = &value
	}
	if sourceIP.Valid {
		session.SourceIP = sourceIP.String
	}
	return session, nil
}
func (s *IdentityStore) ListActiveSessionsForUser(ctx context.Context, userID string, now time.Time) ([]auth.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,user_id::text,token_digest,credential_version,created_at,last_activity_at,expires_at,revoked_at,host(source_ip),user_agent FROM cc_auth_sessions WHERE user_id=$1::uuid AND revoked_at IS NULL AND expires_at>$2 ORDER BY created_at DESC,id ASC`, userID, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]auth.Session, 0)
	for rows.Next() {
		var session auth.Session
		var revoked sql.NullTime
		var sourceIP sql.NullString
		if err := rows.Scan(&session.ID, &session.UserID, &session.TokenDigest, &session.CredentialVersion, &session.CreatedAt, &session.LastActivityAt, &session.ExpiresAt, &revoked, &sourceIP, &session.UserAgent); err != nil {
			return nil, err
		}
		if revoked.Valid {
			value := revoked.Time.UTC()
			session.RevokedAt = &value
		}
		if sourceIP.Valid {
			session.SourceIP = sourceIP.String
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *IdentityStore) TouchSessionByDigest(ctx context.Context, digest string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE cc_auth_sessions SET last_activity_at=GREATEST(last_activity_at,$2) WHERE token_digest=$1 AND revoked_at IS NULL AND expires_at>$2`, digest, at.UTC())
	return requireAffected(result, err, auth.ErrNotFound)
}
func (s *IdentityStore) RevokeSessionByDigest(ctx context.Context, digest string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE cc_auth_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE token_digest=$1`, digest, at.UTC())
	return requireAffected(result, err, auth.ErrNotFound)
}
func (s *IdentityStore) RevokeSessionForUserByID(ctx context.Context, userID, sessionID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE cc_auth_sessions SET revoked_at=$3 WHERE user_id=$1::uuid AND id=$2::uuid AND revoked_at IS NULL AND expires_at>$3`, userID, sessionID, at.UTC())
	return requireAffected(result, err, auth.ErrNotFound)
}
func (s *IdentityStore) RevokeSessionsForUser(ctx context.Context, userID string, at time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE cc_auth_sessions SET revoked_at=$2 WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID, at.UTC())
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}
func BootstrapAdmin(ctx context.Context, db *sql.DB, username, passwordHash string, now time.Time) (string, bool, error) {
	if db == nil || strings.TrimSpace(username) == "" || passwordHash == "" || now.IsZero() {
		return "", false, errors.New("database, username, password hash, and time are required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	userID := deterministicUUID("user:" + strings.ToLower(strings.TrimSpace(username)))
	var insertedID string
	err = tx.QueryRowContext(ctx, `INSERT INTO cc_local_users (id,username,display_name,password_hash,enabled,password_change_required,created_at,updated_at,password_changed_at) VALUES ($1::uuid,$2,'Control Center Administrator',$3,true,true,$4,$4,$4) ON CONFLICT DO NOTHING RETURNING id::text`, userID, strings.TrimSpace(username), passwordHash, now.UTC()).Scan(&insertedID)
	created := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if !created {
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM cc_local_users WHERE lower(username)=lower($1)`, username).Scan(&userID); err != nil {
			return "", false, err
		}
		if err := tx.Commit(); err != nil {
			return "", false, err
		}
		return userID, false, nil
	}
	userID = insertedID
	bindingID := deterministicUUID("binding:" + userID + ":administrator:global")
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_rbac_user_bindings (id,user_id,role_name,scope_kind,scope_id,created_at,created_by) VALUES ($1::uuid,$2::uuid,'administrator','global',NULL,$3,$2::uuid) ON CONFLICT DO NOTHING`, bindingID, userID, now.UTC())
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return userID, true, nil
}
func deterministicUUID(value string) string {
	sum := sha256.Sum256([]byte(value))
	bytes := append([]byte(nil), sum[:16]...)
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:])
}
func requireAffected(result sql.Result, err error, notFound error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return notFound
	}
	return nil
}
