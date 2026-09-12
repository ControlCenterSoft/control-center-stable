package job_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestManualRetryLineageSchemaAndJSONExcludeIdempotencyKey(t *testing.T) {
	path := filepath.Join("..", "..", "..", "api", "operations-job-manual-retry-lineage-v1.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manual retry lineage schema: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode manual retry lineage schema: %v", err)
	}
	if _, exposed := schema.Properties["retry_idempotency_key"]; exposed {
		t.Fatal("manual retry lineage schema must not expose retry idempotency key")
	}

	lineage := job.ManualRetryLineage{
		RootJobID:               "job-root",
		SourceJobID:             "job-source",
		SourceJobVersion:        7,
		RetryJobID:              "job-child",
		RetryIdempotencyKey:     "must-not-project",
		ReviewedAdmissionID:     manualRetryDigestA,
		RevalidationAdmissionID: manualRetryDigestB,
		RevisionID:              "revision-1",
		RevisionDigest:          manualRetryDigestA,
		PolicyID:                "policy-1",
		PolicyDigest:            manualRetryDigestB,
		RetryHistoryDigest:      manualRetryDigestA,
		RequestedAt:             time.Date(2026, 9, 12, 6, 45, 0, 0, time.UTC),
	}
	encoded, err := json.Marshal(lineage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-project") || strings.Contains(string(encoded), "idempotency") {
		t.Fatalf("manual retry lineage leaked idempotency material: %s", encoded)
	}
}
