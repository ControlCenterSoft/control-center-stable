package operationsview

import (
	"slices"
	"testing"
	"time"
)

func TestBuildJobRetryAdmissionEvidenceBlocksAlreadyRetriedSource(t *testing.T) {
	now := time.Date(2026, 9, 12, 7, 20, 0, 0, time.UTC)
	source := failedRetryTestJob(now)
	policy := retryTestPolicy(now)
	evidence, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    source,
		ExpectedJobVersion:     source.Version,
		RevisionID:             "rev-already-retried",
		RevisionDigest:         retryTestDigest,
		Policy:                 policy,
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		SourceAlreadyRetried:   true,
		ObservedAt:             now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != JobRetryAdmissionBlocked || !evidence.SourceAlreadyRetried || !slices.Contains(evidence.Blockers, JobRetryBlockerSourceAlreadyRetried) {
		t.Fatalf("already-retried source was not blocked: %#v", evidence)
	}
}
