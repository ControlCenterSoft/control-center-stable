package migrations

import (
	"strings"
	"testing"
)

func TestManualRetryLineageMigrationIsAdditiveAndPrivacyBounded(t *testing.T) {
	up := readMigration(t, "0012_manual_job_retry_lineage.up.sql")
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS cc_job_manual_retry_lineage",
		"reviewed_admission_id      text        PRIMARY KEY",
		"source_job_id              text        NOT NULL REFERENCES cc_jobs(id)",
		"source_job_version         bigint      NOT NULL CHECK (source_job_version > 0)",
		"retry_job_id               text        NOT NULL UNIQUE REFERENCES cc_jobs(id)",
		"UNIQUE (source_job_id, source_job_version)",
		"request_fingerprint ~ '^[0-9a-f]{64}$'",
		"retry_history_digest ~ '^sha256:[0-9a-f]{64}$'",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("0012 up migration lacks required retry-lineage boundary %q", required)
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
		"CREDENTIAL",
		"SECRET",
		"ACCESS_TOKEN",
	} {
		if strings.Contains(upper, forbidden) {
			t.Fatalf("0012 migration crosses retry-lineage data-minimization boundary: found %q", forbidden)
		}
	}
}

func TestManualRetryLineageDownMigrationIsScoped(t *testing.T) {
	down := strings.ToUpper(readMigration(t, "0012_manual_job_retry_lineage.down.sql"))
	for _, required := range []string{
		"DROP INDEX IF EXISTS CC_JOB_MANUAL_RETRY_ROOT_IDX",
		"DROP TABLE IF EXISTS CC_JOB_MANUAL_RETRY_LINEAGE",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("0012 down migration lacks %q", required)
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
			t.Fatalf("0012 down migration touches earlier owned object %s", protected)
		}
	}
}
