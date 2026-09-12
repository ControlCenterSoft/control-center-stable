package operationsview

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

const retryTestDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func failedRetryTestJob(now time.Time) job.Job {
	return job.Job{
		ID:             "job-retry-1",
		ChangeID:       "chg-retry-1",
		ActionName:     "node.update",
		Input:          json.RawMessage(`{"credential":"must-not-project"}`),
		IdempotencyKey: "private-idempotency-key",
		Status:         job.StatusFailed,
		Attempt:        3,
		MaxAttempts:    3,
		LastError:      "sensitive failure detail must-not-project",
		CreatedAt:      now.Add(-5 * time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
		Version:        7,
	}
}

func retryTestPolicy(now time.Time) JobRetryPolicyEvidence {
	return JobRetryPolicyEvidence{
		PolicyID:          "retry-policy-v1",
		PolicyDigest:      retryTestDigest,
		AllowsManualRetry: true,
		UsedManualRetries: 0,
		MaxManualRetries:  2,
		ObservedAt:        now.Add(-30 * time.Second),
	}
}

func TestBuildJobRetryAdmissionEvidenceEligibleAndDeterministic(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	input := JobRetryAdmissionInput{
		Job:                    failedRetryTestJob(now),
		ExpectedJobVersion:     7,
		RevisionID:             "rev-31-eligible",
		RevisionDigest:         retryTestDigest,
		Policy:                 retryTestPolicy(now),
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	}

	first, err := BuildJobRetryAdmissionEvidence(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildJobRetryAdmissionEvidence(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != JobRetryAdmissionEligible || len(first.Blockers) != 0 {
		t.Fatalf("unexpected admission: %#v", first)
	}
	if first.AdmissionID == "" || first.AdmissionID != second.AdmissionID {
		t.Fatalf("admission identity is not deterministic: %q vs %q", first.AdmissionID, second.AdmissionID)
	}
	if first.JobVersion != input.ExpectedJobVersion || first.SourceAttempt != 3 || first.SourceMaxAttempts != 3 {
		t.Fatalf("source binding lost: %#v", first)
	}
}

func TestBuildJobRetryAdmissionEvidenceRejectsStaleVersion(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	_, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    failedRetryTestJob(now),
		ExpectedJobVersion:     6,
		RevisionID:             "rev-31-stale",
		RevisionDigest:         retryTestDigest,
		Policy:                 retryTestPolicy(now),
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	})
	if !errors.Is(err, job.ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
}

func TestBuildJobRetryAdmissionEvidenceBlocksPolicyAndApprovalGaps(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	policy := retryTestPolicy(now)
	policy.AllowsManualRetry = false
	policy.UsedManualRetries = 2
	policy.MaxManualRetries = 2
	policy.RequiresFreshApproval = true

	evidence, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    failedRetryTestJob(now),
		ExpectedJobVersion:     7,
		RevisionID:             "rev-31-blocked",
		RevisionDigest:         retryTestDigest,
		Policy:                 policy,
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != JobRetryAdmissionBlocked {
		t.Fatalf("state = %q, want blocked", evidence.State)
	}
	for _, blocker := range []string{
		JobRetryBlockerPolicyDenied,
		JobRetryBlockerManualBudgetExhausted,
		JobRetryBlockerFreshApprovalRequired,
	} {
		if !slices.Contains(evidence.Blockers, blocker) {
			t.Fatalf("missing blocker %q in %#v", blocker, evidence.Blockers)
		}
	}
}

func TestBuildJobRetryAdmissionEvidenceAcceptsBoundApprovalDigest(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	policy := retryTestPolicy(now)
	policy.RequiresFreshApproval = true
	policy.ApprovalEvidenceDigest = retryTestDigest

	evidence, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    failedRetryTestJob(now),
		ExpectedJobVersion:     7,
		RevisionID:             "rev-31-approved",
		RevisionDigest:         retryTestDigest,
		Policy:                 policy,
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != JobRetryAdmissionEligible || len(evidence.Blockers) != 0 {
		t.Fatalf("approved admission unexpectedly blocked: %#v", evidence)
	}
	if evidence.ApprovalEvidenceDigest != retryTestDigest {
		t.Fatalf("approval digest binding lost: %#v", evidence)
	}
}

func TestBuildJobRetryAdmissionEvidenceBlocksNonFailedSource(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	source := failedRetryTestJob(now)
	source.Status = job.StatusSucceeded
	source.Attempt = 1

	evidence, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    source,
		ExpectedJobVersion:     source.Version,
		RevisionID:             "rev-31-success",
		RevisionDigest:         retryTestDigest,
		Policy:                 retryTestPolicy(now),
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != JobRetryAdmissionBlocked || !slices.Contains(evidence.Blockers, JobRetryBlockerSourceNotFailed) {
		t.Fatalf("non-failed source was not blocked: %#v", evidence)
	}
}

func TestBuildJobRetryAdmissionEvidenceRejectsInconsistentFailedAttemptState(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	source := failedRetryTestJob(now)
	source.Attempt = 2

	_, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    source,
		ExpectedJobVersion:     source.Version,
		RevisionID:             "rev-31-invalid-attempt",
		RevisionDigest:         retryTestDigest,
		Policy:                 retryTestPolicy(now),
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	})
	if !errors.Is(err, ErrInvalidJobRetryAdmission) {
		t.Fatalf("invalid failed-attempt state error = %v", err)
	}
}

func TestBuildJobRetryAdmissionEvidenceRejectsHistoryOlderThanSourceJob(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	source := failedRetryTestJob(now)

	_, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    source,
		ExpectedJobVersion:     source.Version,
		RevisionID:             "rev-31-stale-history",
		RevisionDigest:         retryTestDigest,
		Policy:                 retryTestPolicy(now),
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: source.UpdatedAt.Add(-time.Second),
		ObservedAt:             now,
	})
	if !errors.Is(err, ErrInvalidJobRetryAdmission) {
		t.Fatalf("stale retry history error = %v", err)
	}
}

func TestJobRetryAdmissionEvidenceDoesNotProjectSensitiveJobPayload(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 20, 0, 0, time.UTC)
	evidence, err := BuildJobRetryAdmissionEvidence(JobRetryAdmissionInput{
		Job:                    failedRetryTestJob(now),
		ExpectedJobVersion:     7,
		RevisionID:             "rev-31-redaction",
		RevisionDigest:         retryTestDigest,
		Policy:                 retryTestPolicy(now),
		RetryHistoryDigest:     retryTestDigest,
		RetryHistoryObservedAt: now.Add(-20 * time.Second),
		ObservedAt:             now,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"must-not-project", "private-idempotency-key", "sensitive failure detail"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("retry evidence leaked sensitive source material %q: %s", forbidden, encoded)
		}
	}
}
