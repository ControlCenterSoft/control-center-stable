package operationsview

import (
	"errors"
	"testing"
	"time"
)

const maintenanceWindowTestDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestBuildMaintenanceWindowEvidenceStates(t *testing.T) {
	start := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	window := MaintenanceWindow{StartsAt: start, EndsAt: start.Add(time.Hour)}
	tests := []struct {
		name string
		now  time.Time
		want MaintenanceWindowState
	}{
		{name: "scheduled", now: start.Add(-time.Second), want: MaintenanceWindowScheduled},
		{name: "open at start", now: start, want: MaintenanceWindowOpen},
		{name: "open before end", now: start.Add(time.Hour - time.Nanosecond), want: MaintenanceWindowOpen},
		{name: "expired at end", now: start.Add(time.Hour), want: MaintenanceWindowExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence, err := BuildMaintenanceWindowEvidence(
				"change-31", "revision-7", maintenanceWindowTestDigest, true, &window, tt.now,
			)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.State != tt.want {
				t.Fatalf("state = %q, want %q", evidence.State, tt.want)
			}
			if evidence.ExecutionAuthorized || evidence.ProductionMutationAllowed {
				t.Fatal("maintenance window evidence must not grant mutation authority")
			}
			if evidence.Window == nil || !evidence.Window.StartsAt.Equal(start) {
				t.Fatal("normalized window is missing or changed")
			}
		})
	}
}

func TestBuildMaintenanceWindowEvidenceNotRequired(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	evidence, err := BuildMaintenanceWindowEvidence(
		"change-31", "revision-7", maintenanceWindowTestDigest, false, nil, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != MaintenanceWindowNotRequired || evidence.Window != nil {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}
}

func TestBuildMaintenanceWindowEvidenceRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	valid := MaintenanceWindow{StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour)}
	tests := []struct {
		name     string
		changeID string
		revision string
		digest   string
		required bool
		window   *MaintenanceWindow
		evalAt   time.Time
	}{
		{name: "empty change", revision: "revision-7", digest: maintenanceWindowTestDigest, required: true, window: &valid, evalAt: now},
		{name: "trimmed revision", changeID: "change-31", revision: " revision-7", digest: maintenanceWindowTestDigest, required: true, window: &valid, evalAt: now},
		{name: "bad digest", changeID: "change-31", revision: "revision-7", digest: "sha256:BAD", required: true, window: &valid, evalAt: now},
		{name: "missing required window", changeID: "change-31", revision: "revision-7", digest: maintenanceWindowTestDigest, required: true, evalAt: now},
		{name: "unexpected optional window", changeID: "change-31", revision: "revision-7", digest: maintenanceWindowTestDigest, window: &valid, evalAt: now},
		{name: "zero evaluation time", changeID: "change-31", revision: "revision-7", digest: maintenanceWindowTestDigest, required: true, window: &valid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildMaintenanceWindowEvidence(tt.changeID, tt.revision, tt.digest, tt.required, tt.window, tt.evalAt)
			if !errors.Is(err, ErrInvalidMaintenanceWindowEvidence) {
				t.Fatalf("error = %v, want ErrInvalidMaintenanceWindowEvidence", err)
			}
		})
	}
}

func TestBuildMaintenanceWindowEvidenceRejectsUnsafeDuration(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	for _, window := range []MaintenanceWindow{
		{StartsAt: now, EndsAt: now.Add(MinimumMaintenanceWindowDuration - time.Nanosecond)},
		{StartsAt: now, EndsAt: now.Add(MaximumMaintenanceWindowDuration + time.Nanosecond)},
		{StartsAt: now, EndsAt: now},
	} {
		_, err := BuildMaintenanceWindowEvidence(
			"change-31", "revision-7", maintenanceWindowTestDigest, true, &window, now,
		)
		if !errors.Is(err, ErrInvalidMaintenanceWindowEvidence) {
			t.Fatalf("error = %v, want ErrInvalidMaintenanceWindowEvidence", err)
		}
	}
}

func TestBuildMaintenanceWindowEvidenceAcceptsPreflightMinimumDuration(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC)
	window := MaintenanceWindow{StartsAt: now, EndsAt: now.Add(time.Minute)}
	evidence, err := BuildMaintenanceWindowEvidence(
		"change-31", "revision-7", maintenanceWindowTestDigest, true, &window, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.State != MaintenanceWindowOpen {
		t.Fatalf("state = %q, want %q", evidence.State, MaintenanceWindowOpen)
	}
}
