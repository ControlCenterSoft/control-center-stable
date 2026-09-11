package networkpolicy

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseVerificationEvidenceAcceptsCanonicalDocument(t *testing.T) {
	now := time.Date(2026, 9, 11, 17, 40, 0, 0, time.UTC)
	evidence := validVerificationEvidence(now)
	document, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseVerificationEvidence(document)
	if err != nil {
		t.Fatalf("ParseVerificationEvidence() error = %v", err)
	}
	if parsed.PlanID != evidence.PlanID || parsed.RevisionID != evidence.RevisionID || len(parsed.Checks) != len(evidence.Checks) {
		t.Fatalf("parsed evidence = %#v, want binding %#v", parsed, evidence)
	}
}

func TestParseVerificationEvidenceRejectsUnknownAndTrailingJSON(t *testing.T) {
	now := time.Date(2026, 9, 11, 17, 40, 0, 0, time.UTC)
	evidence := validVerificationEvidence(now)

	object := map[string]any{}
	canonical, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object["command"] = "ip link set"
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationEvidence(unknown); err == nil {
		t.Fatal("unknown execution-shaped field was accepted")
	}

	trailing := append(append([]byte(nil), canonical...), []byte("\n{}")...)
	if _, err := ParseVerificationEvidence(trailing); err == nil {
		t.Fatal("trailing JSON document was accepted")
	}
}

func TestParseVerificationEvidenceRejectsDuplicateAndNonCanonicalBindings(t *testing.T) {
	now := time.Date(2026, 9, 11, 17, 40, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(*VerificationEvidence)
	}{
		{
			name: "duplicate check",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks = append(evidence.Checks, evidence.Checks[0])
			},
		},
		{
			name: "non canonical revision",
			mutate: func(evidence *VerificationEvidence) {
				evidence.RevisionID = " " + evidence.RevisionID + " "
			},
		},
		{
			name: "non canonical check",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks[0].Name = " " + evidence.Checks[0].Name
			},
		},
		{
			name: "non canonical digest",
			mutate: func(evidence *VerificationEvidence) {
				evidence.Checks[0].EvidenceDigest += " "
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validVerificationEvidence(now)
			test.mutate(&evidence)
			document, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseVerificationEvidence(document); err == nil {
				t.Fatal("invalid serialized verification evidence was accepted")
			}
		})
	}
}
