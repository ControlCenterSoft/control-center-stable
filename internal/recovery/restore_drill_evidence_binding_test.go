package recovery

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRestoreDrillEvidenceBindingBindsExactStoredEvidence(t *testing.T) {
	restore := validRestoreMetadata()
	assessment, err := EvaluateRestoreDrillFreshness(
		restore,
		24*time.Hour,
		restore.Verification.VerifiedAt.Add(6*time.Hour),
	)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}

	binding, err := BuildRestoreDrillEvidenceBinding(restore, assessment)
	if err != nil {
		t.Fatalf("BuildRestoreDrillEvidenceBinding() error = %v", err)
	}
	if binding.RestoreID != restore.ObjectID || binding.RestoreResourceVersion != restore.ResourceVersion || binding.RestoreGeneration != restore.Generation {
		t.Fatalf("binding lost exact restore identity: %#v", binding)
	}
	if binding.AssessmentID != assessment.AssessmentID || !binding.VerifiedAt.Equal(assessment.VerifiedAt) {
		t.Fatalf("binding lost freshness identity: %#v", binding)
	}
	if !strings.HasPrefix(binding.VerificationEvidenceDigest, "sha256:") || len(binding.VerificationEvidenceDigest) != len("sha256:")+64 {
		t.Fatalf("unexpected evidence digest: %q", binding.VerificationEvidenceDigest)
	}
	if !binding.AdvisoryOnly || binding.ProductionMutation {
		t.Fatalf("binding unexpectedly grants mutation authority: %#v", binding)
	}

	changed := restore
	changed.Verification.Evidence = append([]EvidenceReference(nil), restore.Verification.Evidence...)
	changed.Verification.Evidence[0].Digest = "sha256:" + strings.Repeat("b", 64)
	changedBinding, err := BuildRestoreDrillEvidenceBinding(changed, assessment)
	if err != nil {
		t.Fatalf("BuildRestoreDrillEvidenceBinding(changed evidence) error = %v", err)
	}
	if changedBinding.VerificationEvidenceDigest == binding.VerificationEvidenceDigest {
		t.Fatal("different verification evidence produced the same evidence digest")
	}
	if changedBinding.BindingID == binding.BindingID {
		t.Fatal("different verification evidence produced the same binding identity")
	}
}

func TestBuildRestoreDrillEvidenceBindingCanonicalizesEvidenceOrder(t *testing.T) {
	restore := validRestoreMetadata()
	assessment, err := EvaluateRestoreDrillFreshness(
		restore,
		12*time.Hour,
		restore.Verification.VerifiedAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}
	left, err := BuildRestoreDrillEvidenceBinding(restore, assessment)
	if err != nil {
		t.Fatalf("left binding error = %v", err)
	}

	reordered := restore
	reordered.Verification.Evidence = append([]EvidenceReference(nil), restore.Verification.Evidence...)
	for i, j := 0, len(reordered.Verification.Evidence)-1; i < j; i, j = i+1, j-1 {
		reordered.Verification.Evidence[i], reordered.Verification.Evidence[j] = reordered.Verification.Evidence[j], reordered.Verification.Evidence[i]
	}
	right, err := BuildRestoreDrillEvidenceBinding(reordered, assessment)
	if err != nil {
		t.Fatalf("right binding error = %v", err)
	}
	if left.VerificationEvidenceDigest != right.VerificationEvidenceDigest || left.BindingID != right.BindingID {
		t.Fatalf("evidence order changed canonical identity: left=%#v right=%#v", left, right)
	}
}

func TestBuildRestoreDrillEvidenceBindingRejectsAssessmentDrift(t *testing.T) {
	restore := validRestoreMetadata()
	assessment, err := EvaluateRestoreDrillFreshness(
		restore,
		24*time.Hour,
		restore.Verification.VerifiedAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}

	assessment.State = RestoreDrillStale
	if _, err := BuildRestoreDrillEvidenceBinding(restore, assessment); err == nil {
		t.Fatal("tampered freshness assessment accepted")
	}
}

func TestBuildRestoreDrillEvidenceBindingIncludesResourceVersion(t *testing.T) {
	restore := validRestoreMetadata()
	assessment, err := EvaluateRestoreDrillFreshness(
		restore,
		24*time.Hour,
		restore.Verification.VerifiedAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}
	left, err := BuildRestoreDrillEvidenceBinding(restore, assessment)
	if err != nil {
		t.Fatalf("left binding error = %v", err)
	}

	updated := restore
	updated.ResourceVersion = restore.ResourceVersion + ":next"
	right, err := BuildRestoreDrillEvidenceBinding(updated, assessment)
	if err != nil {
		t.Fatalf("right binding error = %v", err)
	}
	if left.BindingID == right.BindingID {
		t.Fatal("resource-version drift did not change binding identity")
	}
}

func TestBuildRestoreDrillEvidenceBindingIsDeterministic(t *testing.T) {
	restore := validRestoreMetadata()
	assessment, err := EvaluateRestoreDrillFreshness(
		restore,
		24*time.Hour,
		restore.Verification.VerifiedAt.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}

	first, err := BuildRestoreDrillEvidenceBinding(restore, assessment)
	if err != nil {
		t.Fatalf("first binding error = %v", err)
	}
	second, err := BuildRestoreDrillEvidenceBinding(restore, assessment)
	if err != nil {
		t.Fatalf("second binding error = %v", err)
	}
	if first != second {
		t.Fatalf("identical restore evidence produced non-deterministic binding: first=%#v second=%#v", first, second)
	}
}

func TestBuildRestoreDrillEvidenceBindingRejectsInvalidFreshnessWindow(t *testing.T) {
	restore := validRestoreMetadata()
	assessment, err := EvaluateRestoreDrillFreshness(
		restore,
		24*time.Hour,
		restore.Verification.VerifiedAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}

	assessment.MaxAgeSeconds = 0
	if _, err := BuildRestoreDrillEvidenceBinding(restore, assessment); err == nil {
		t.Fatal("zero max_age_seconds accepted")
	}
}
