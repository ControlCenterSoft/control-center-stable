package migrations

import (
	"strings"
	"testing"
)

func TestStable030To031MigrationBoundaryIsExplicit(t *testing.T) {
	all := migrationFiles(t, "*.up.sql")
	if len(all) < len(stable030Up)+1 {
		t.Fatalf("migration set has %d up migrations, want at least %d", len(all), len(stable030Up)+1)
	}
	for index, want := range stable030Up {
		if got := all[index]; got != want {
			t.Fatalf("Stable 0.30 migration boundary changed at index %d: got %q want %q", index, got, want)
		}
	}
	if got := all[len(stable030Up)]; got != "0011_job_timeline.up.sql" {
		t.Fatalf("first post-0.30 migration = %q, want 0011_job_timeline.up.sql", got)
	}
}

func TestJobTimelineMigrationIsAdditiveAndDataMinimized(t *testing.T) {
	up := readMigration(t, "0011_job_timeline.up.sql")
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS cc_job_timeline",
		"PRIMARY KEY (job_id, job_version)",
		"REFERENCES cc_jobs(id) ON DELETE CASCADE",
		"INSERT INTO cc_job_timeline (job_id, job_version, event, status, attempt, occurred_at)",
		"SELECT id, version, 'observed', status, attempt, updated_at",
		"ON CONFLICT (job_id, job_version) DO NOTHING",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("0011 up migration lacks required additive boundary %q", required)
		}
	}

	upper := strings.ToUpper(up)
	for _, forbidden := range []string{
		"ALTER TABLE CC_JOBS",
		"UPDATE CC_JOBS",
		"DELETE FROM CC_JOBS",
		"DROP TABLE CC_JOBS",
		"TRUNCATE CC_JOBS",
		"LEASE_TOKEN",
		"LEASE_WORKER_ID",
		"LAST_ERROR",
		"OUTPUT",
		"INPUT_FINGERPRINT",
	} {
		if strings.Contains(upper, forbidden) {
			t.Fatalf("0011 migration crosses Stable 0.30 data-minimization boundary: found %q", forbidden)
		}
	}
}

func TestJobTimelineDownMigrationIsScoped(t *testing.T) {
	down := strings.ToUpper(readMigration(t, "0011_job_timeline.down.sql"))
	for _, required := range []string{
		"DROP INDEX IF EXISTS CC_JOB_TIMELINE_ORDER_IDX",
		"DROP TABLE IF EXISTS CC_JOB_TIMELINE",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("0011 down migration lacks %q", required)
		}
	}
	for _, protected := range []string{
		"CC_JOBS",
		"CC_CHANGES",
		"CC_CONFIG_REVISIONS",
		"CC_LOCAL_USERS",
		"CC_AUTH_SESSIONS",
		"CC_AUDIT_EVENTS",
	} {
		if strings.Contains(down, protected) {
			t.Fatalf("0011 down migration touches Stable 0.30-owned object %s", protected)
		}
	}
}
