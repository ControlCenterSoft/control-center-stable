package networkpolicy

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvaluateVerificationFreshnessRejectsNonCanonicalEvidenceEncoding(t *testing.T) {
	now := time.Date(2026, 9, 11, 17, 35, 0, 0, time.UTC)
	policy := VerificationFreshnessPolicy{
		MaxAge:         10 * time.Minute,
		MaxFutureSkew:  30 * time.Second,
		RequiredChecks: []string{"control_plane", "link_state"},
	}

	tests := []struct {
		name   string
		mutate func(*VerificationEvidence)
	}{
		{
			name: "revision whitespace",
			mutate: func(evidence *VerificationEvidence) {
				evidence.RevisionID = " " + evidence.RevisionID + " "
			},
		},
		{
			name: "check name whitespace",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks[0].Name = " " + evidence.Checks[0].Name + " "
			},
		},
		{
			name: "digest whitespace",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks[0].EvidenceDigest = " " + evidence.Checks[0].EvidenceDigest
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validVerificationEvidence(now)
			test.mutate(&evidence)
			if verdict, err := EvaluateVerificationFreshness(
				now,
				evidence.PlanID,
				"rev-028",
				evidence,
				policy,
			); err == nil {
				t.Fatalf("non-canonical evidence accepted: %#v", verdict)
			}
		})
	}
}

func TestParseVerificationPreflightAdmissionRejectsNonCanonicalRevision(t *testing.T) {
	plan, _, snapshot, now := preflightFixture(t)
	evidence := planVerificationEvidence(plan, now.Add(-time.Second))
	admission, err := BuildVerificationPreflightAdmission(
		now,
		plan,
		snapshot,
		evidence,
		ChangePlanVerificationFreshnessPolicy{MaxAge: 20 * time.Second, MaxFutureSkew: time.Second},
	)
	if err != nil {
		t.Fatalf("BuildVerificationPreflightAdmission() error = %v", err)
	}

	admission.RevisionID = " " + admission.RevisionID + " "
	admission.AdmissionID = verificationPreflightAdmissionIdentity(admission)
	document, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("non-canonical admission revision_id was accepted")
	}
}
