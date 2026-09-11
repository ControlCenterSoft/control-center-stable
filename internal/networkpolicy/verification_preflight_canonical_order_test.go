package networkpolicy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseVerificationPreflightAdmissionRejectsUnsortedReasonList(t *testing.T) {
	admission := VerificationPreflightAdmission{
		SchemaVersion:             VerificationPreflightAdmissionSchemaVersion,
		PlanID:                    "sha256:" + strings.Repeat("a", 64),
		RevisionID:                "network-revision-028",
		EvidenceID:                "sha256:" + strings.Repeat("b", 64),
		MachineVersion:            2,
		EvaluatedAt:               time.Date(2026, 9, 11, 17, 25, 0, 0, time.UTC),
		Ready:                     false,
		MissingChecks:             []string{"probe-b", "probe-a"},
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	admission.AdmissionID = verificationPreflightAdmissionIdentity(admission)

	document, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("unsorted rejection reasons were accepted")
	}
}
