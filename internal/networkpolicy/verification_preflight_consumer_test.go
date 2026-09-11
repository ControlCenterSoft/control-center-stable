package networkpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseVerificationPreflightAdmissionAcceptsCanonicalFixture(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("testdata", "network_change_verification_preflight_admission_valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	admission, err := ParseVerificationPreflightAdmission(document)
	if err != nil {
		t.Fatalf("ParseVerificationPreflightAdmission() error = %v", err)
	}
	if !admission.Ready || admission.MachineVersion != 2 {
		t.Fatalf("parsed admission = %#v", admission)
	}
	if admission.ExecutionAuthorized || admission.ProductionMutationAllowed {
		t.Fatalf("fixture unexpectedly grants mutation authority: %#v", admission)
	}
}

func TestParseVerificationPreflightAdmissionRejectsUnknownFieldFixture(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("testdata", "network_change_verification_preflight_admission_unknown_field.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("unknown execution field was accepted")
	}
}

func TestParseVerificationPreflightAdmissionRejectsOversizedReasonList(t *testing.T) {
	checks := make([]string, maxVerificationChecks+1)
	for index := range checks {
		checks[index] = fmt.Sprintf("probe-%03d", index)
	}
	admission := VerificationPreflightAdmission{
		SchemaVersion:             VerificationPreflightAdmissionSchemaVersion,
		AdmissionID:               "sha256:" + strings.Repeat("c", 64),
		PlanID:                    "sha256:" + strings.Repeat("a", 64),
		RevisionID:                "network-revision-028",
		EvidenceID:                "sha256:" + strings.Repeat("b", 64),
		MachineVersion:            2,
		EvaluatedAt:               time.Date(2026, 9, 11, 10, 0, 5, 0, time.UTC),
		Ready:                     false,
		StaleChecks:               checks,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	document, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("oversized stale_checks list was accepted")
	}
}

func TestParseVerificationPreflightAdmissionRejectsDuplicateReason(t *testing.T) {
	admission := VerificationPreflightAdmission{
		SchemaVersion:             VerificationPreflightAdmissionSchemaVersion,
		AdmissionID:               "sha256:" + strings.Repeat("c", 64),
		PlanID:                    "sha256:" + strings.Repeat("a", 64),
		RevisionID:                "network-revision-028",
		EvidenceID:                "sha256:" + strings.Repeat("b", 64),
		MachineVersion:            2,
		EvaluatedAt:               time.Date(2026, 9, 11, 10, 0, 5, 0, time.UTC),
		Ready:                     false,
		MissingChecks:             []string{"probe-a", "probe-a"},
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	document, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("duplicate rejection reason was accepted")
	}
}

func TestParseVerificationPreflightAdmissionRejectsReasonlessRejectedAdmission(t *testing.T) {
	admission := VerificationPreflightAdmission{
		SchemaVersion:             VerificationPreflightAdmissionSchemaVersion,
		AdmissionID:               "sha256:" + strings.Repeat("c", 64),
		PlanID:                    "sha256:" + strings.Repeat("a", 64),
		RevisionID:                "network-revision-028",
		EvidenceID:                "sha256:" + strings.Repeat("b", 64),
		MachineVersion:            2,
		EvaluatedAt:               time.Date(2026, 9, 11, 10, 0, 5, 0, time.UTC),
		Ready:                     false,
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	document, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("rejected admission without a rejection reason was accepted")
	}
}

func TestParseVerificationPreflightAdmissionRejectsExpiryOnRejectedAdmission(t *testing.T) {
	admission := VerificationPreflightAdmission{
		SchemaVersion:             VerificationPreflightAdmissionSchemaVersion,
		AdmissionID:               "sha256:" + strings.Repeat("c", 64),
		PlanID:                    "sha256:" + strings.Repeat("a", 64),
		RevisionID:                "network-revision-028",
		EvidenceID:                "sha256:" + strings.Repeat("b", 64),
		MachineVersion:            2,
		EvaluatedAt:               time.Date(2026, 9, 11, 10, 0, 5, 0, time.UTC),
		ExpiresAt:                 time.Date(2026, 9, 11, 10, 0, 20, 0, time.UTC),
		Ready:                     false,
		StaleChecks:               []string{"probe-a"},
		ExecutionAuthorized:       false,
		ProductionMutationAllowed: false,
	}
	document, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("rejected admission with expires_at was accepted")
	}
}

func TestParseVerificationPreflightAdmissionRejectsTrailingJSON(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("testdata", "network_change_verification_preflight_admission_valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	document = append(document, []byte("\n{}")...)
	if _, err := ParseVerificationPreflightAdmission(document); err == nil {
		t.Fatal("trailing JSON document was accepted")
	}
}
