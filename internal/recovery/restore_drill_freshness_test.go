package recovery

import (
	"testing"
	"time"
)

func TestEvaluateRestoreDrillFreshnessFresh(t *testing.T) {
	restore := validRestoreMetadata()
	checked := restore.Verification.VerifiedAt.Add(6 * time.Hour)
	got, err := EvaluateRestoreDrillFreshness(restore, 24*time.Hour, checked)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}
	if got.State != RestoreDrillFresh || got.AssessmentID == "" {
		t.Fatalf("unexpected assessment: %#v", got)
	}
	if got.RestoreID != restore.ObjectID || got.ValidUntil != restore.Verification.VerifiedAt.UTC().Truncate(time.Second).Add(24*time.Hour) {
		t.Fatalf("assessment lost restore lineage: %#v", got)
	}
	if !got.AdvisoryOnly || got.ProductionMutation {
		t.Fatalf("freshness assessment unexpectedly grants mutation authority: %#v", got)
	}
}

func TestEvaluateRestoreDrillFreshnessStaleAtBoundary(t *testing.T) {
	restore := validRestoreMetadata()
	checked := restore.Verification.VerifiedAt.Add(24 * time.Hour)
	got, err := EvaluateRestoreDrillFreshness(restore, 24*time.Hour, checked)
	if err != nil {
		t.Fatalf("EvaluateRestoreDrillFreshness() error = %v", err)
	}
	if got.State != RestoreDrillStale {
		t.Fatalf("expected stale assessment at validity boundary: %#v", got)
	}
}

func TestEvaluateRestoreDrillFreshnessRejectsNonDrill(t *testing.T) {
	restore := validRestoreMetadata()
	restore.Mode = RestoreAlternate
	restore.Provider.Capabilities = []ProviderCapability{CapabilityRestore}
	restore.Verification.Evidence = []EvidenceReference{testEvidence("restore-proof", EvidenceChecksum, *restore.Verification.VerifiedAt)}
	if _, err := EvaluateRestoreDrillFreshness(restore, 24*time.Hour, restore.Verification.VerifiedAt.Add(time.Hour)); err == nil {
		t.Fatal("non-drill restore accepted as drill freshness evidence")
	}
}

func TestEvaluateRestoreDrillFreshnessRejectsInvalidTimeWindow(t *testing.T) {
	restore := validRestoreMetadata()
	for _, maxAge := range []time.Duration{0, time.Second + time.Nanosecond, 366 * 24 * time.Hour} {
		if _, err := EvaluateRestoreDrillFreshness(restore, maxAge, restore.Verification.VerifiedAt.Add(time.Hour)); err == nil {
			t.Fatalf("invalid max age accepted: %v", maxAge)
		}
	}
	if _, err := EvaluateRestoreDrillFreshness(restore, time.Hour, restore.Verification.VerifiedAt.Add(-time.Second)); err == nil {
		t.Fatal("checked_at before verified_at accepted")
	}
}

func TestEvaluateRestoreDrillFreshnessIdentityDeterministic(t *testing.T) {
	restore := validRestoreMetadata()
	checked := restore.Verification.VerifiedAt.Add(time.Hour)
	left, err := EvaluateRestoreDrillFreshness(restore, 12*time.Hour, checked)
	if err != nil {
		t.Fatalf("left assessment: %v", err)
	}
	right, err := EvaluateRestoreDrillFreshness(restore, 12*time.Hour, checked)
	if err != nil {
		t.Fatalf("right assessment: %v", err)
	}
	if left.AssessmentID != right.AssessmentID {
		t.Fatalf("assessment identity is not deterministic: %q != %q", left.AssessmentID, right.AssessmentID)
	}
}
