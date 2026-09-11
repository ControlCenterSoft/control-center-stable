package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"control-center/internal/identity/auth"
	"control-center/internal/identity/security"
)

func TestPostgresSessionActivityPersistsAndAdvances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL session activity test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var activityColumnPresent bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name='cc_auth_sessions' AND column_name='last_activity_at'
	)`).Scan(&activityColumnPresent); err != nil || !activityColumnPresent {
		t.Fatalf("database must be migrated through 0009: present=%v err=%v", activityColumnPresent, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := fmt.Sprintf("%d", now.UnixNano())
	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash("session-activity-integration-password")
	if err != nil {
		t.Fatal(err)
	}
	userID, created, err := BootstrapAdmin(ctx, db, "session-activity-"+suffix, passwordHash, now)
	if err != nil || !created {
		t.Fatalf("bootstrap created=%v err=%v", created, err)
	}
	defer db.ExecContext(context.Background(), `DELETE FROM cc_local_users WHERE id=$1::uuid`, userID)

	store, err := NewIdentityStore(db)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.FindUserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256([]byte("session-activity-" + suffix))
	digest := hex.EncodeToString(digestBytes[:])
	session := auth.Session{
		ID:                deterministicUUID("session-activity:" + suffix),
		UserID:            userID,
		TokenDigest:       digest,
		CredentialVersion: user.PasswordChangedAt,
		CreatedAt:         now,
		LastActivityAt:    now,
		ExpiresAt:         now.Add(8 * time.Hour),
		SourceIP:          "192.0.2.90",
		UserAgent:         "session-activity-integration-test",
	}
	if err := store.CreateSession(ctx, session, passwordHash); err != nil {
		t.Fatal(err)
	}

	persisted, err := store.FindSessionByDigest(ctx, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastActivityAt.Equal(now) {
		t.Fatalf("last activity=%s, want %s", persisted.LastActivityAt, now)
	}

	advanced := now.Add(5 * time.Minute)
	if err := store.TouchSessionByDigest(ctx, digest, advanced); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchSessionByDigest(ctx, digest, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("stale concurrent activity should be an idempotent no-op: %v", err)
	}
	persisted, err = store.FindSessionByDigest(ctx, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.LastActivityAt.Equal(advanced) {
		t.Fatalf("advanced last activity=%s, want %s", persisted.LastActivityAt, advanced)
	}
}
