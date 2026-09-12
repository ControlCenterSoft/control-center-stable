package job_test

import (
	"testing"
	"time"

	"control-center/internal/orchestration/job"
)

func TestManualRetryRequestFingerprintIgnoresTransportTimeAndFreshRevalidationID(t *testing.T) {
	now := time.Date(2026, 9, 12, 7, 30, 0, 0, time.UTC)
	request := job.ManualRetryRequest{
		SourceJobID:             "job-source",
		ExpectedSourceVersion:   7,
		RetryJobID:              "job-child",
		RetryIdempotencyKey:     "retry-child-key",
		ReviewedAdmissionID:     manualRetryDigestA,
		RevalidationAdmissionID: manualRetryDigestB,
		RevisionID:              "revision-7",
		RevisionDigest:          manualRetryDigestA,
		PolicyID:                "policy-7",
		PolicyDigest:            manualRetryDigestB,
		RetryHistoryDigest:      manualRetryDigestA,
		ApprovalEvidenceDigest:  manualRetryDigestB,
		RequestedAt:             now,
	}
	first, err := job.ManualRetryRequestFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	request.RequestedAt = now.Add(time.Minute)
	request.RevalidationAdmissionID = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	second, err := job.ManualRetryRequestFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("transport replay changed logical fingerprint: first=%q second=%q", first, second)
	}
}

func TestManualRetryRequestFingerprintBindsSemanticLineageIdentity(t *testing.T) {
	now := time.Date(2026, 9, 12, 7, 35, 0, 0, time.UTC)
	base := job.ManualRetryRequest{
		SourceJobID:             "job-source",
		ExpectedSourceVersion:   7,
		RetryJobID:              "job-child",
		RetryIdempotencyKey:     "retry-child-key",
		ReviewedAdmissionID:     manualRetryDigestA,
		RevalidationAdmissionID: manualRetryDigestB,
		RevisionID:              "revision-7",
		RevisionDigest:          manualRetryDigestA,
		PolicyID:                "policy-7",
		PolicyDigest:            manualRetryDigestB,
		RetryHistoryDigest:      manualRetryDigestA,
		RequestedAt:             now,
	}
	want, err := job.ManualRetryRequestFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*job.ManualRetryRequest){
		"source version": func(v *job.ManualRetryRequest) { v.ExpectedSourceVersion++ },
		"child id":       func(v *job.ManualRetryRequest) { v.RetryJobID = "job-child-other" },
		"idempotency":    func(v *job.ManualRetryRequest) { v.RetryIdempotencyKey = "retry-other-key" },
		"revision":       func(v *job.ManualRetryRequest) { v.RevisionDigest = manualRetryDigestB },
		"policy":         func(v *job.ManualRetryRequest) { v.PolicyDigest = manualRetryDigestA },
		"history": func(v *job.ManualRetryRequest) {
			v.RetryHistoryDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			got, err := job.ManualRetryRequestFingerprint(changed)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("%s did not change logical fingerprint", name)
			}
		})
	}
}
