package migrations

import (
	"strings"
	"testing"
)

func TestSessionActivityMigrationContract(t *testing.T) {
	up := readMigration(t, "0009_auth_session_activity.up.sql")
	for _, fragment := range []string{
		"ADD COLUMN last_activity_at timestamptz",
		"SET last_activity_at = created_at",
		"ALTER COLUMN last_activity_at SET NOT NULL",
		"last_activity_at >= created_at",
		"last_activity_at <= expires_at",
		"WHERE revoked_at IS NULL",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("0009 up migration lacks %q", fragment)
		}
	}

	down := readMigration(t, "0009_auth_session_activity.down.sql")
	for _, fragment := range []string{
		"DROP INDEX IF EXISTS cc_auth_sessions_user_activity_idx",
		"DROP CONSTRAINT IF EXISTS cc_auth_sessions_activity_window",
		"DROP COLUMN IF EXISTS last_activity_at",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("0009 down migration lacks %q", fragment)
		}
	}
}
