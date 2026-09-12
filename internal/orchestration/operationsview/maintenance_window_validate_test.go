package operationsview

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateMaintenanceWindowEvidenceAcceptsBuiltContract(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
	window := MaintenanceWindow{StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)}
	evidence, err := BuildMaintenanceWindowEvidence(
		"change-a",
		"revision-a",
		"sha256:"+strings.Repeat("a", 64),
		true,
		&window,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaintenanceWindowEvidence(evidence); err != nil {
		t.Fatalf("built evidence must validate: %v", err)
	}
}

func TestValidateMaintenanceWindowEvidenceRejectsTampering(t *testing.T) {
	now := time.Date(2026, 9, 12, 2, 40, 0, 0, time.UTC)
	window := MaintenanceWindow{StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour)}
	base, err := BuildMaintenanceWindowEvidence(
		"change-a",
		"revision-a",
		"sha256:"+strings.Repeat("b", 64),
		true,
		&window,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*MaintenanceWindowEvidence)
	}{
		{name: "contract", mutate: func(item *MaintenanceWindowEvidence) { item.ContractVersion = "unknown/v1" }},
		{name: "state", mutate: func(item *MaintenanceWindowEvidence) { item.State = MaintenanceWindowOpen }},
		{name: "execution authority", mutate: func(item *MaintenanceWindowEvidence) { item.ExecutionAuthorized = true }},
		{name: "production mutation", mutate: func(item *MaintenanceWindowEvidence) { item.ProductionMutationAllowed = true }},
		{name: "digest", mutate: func(item *MaintenanceWindowEvidence) { item.RevisionDigest = "sha256:" + strings.Repeat("C", 64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := base
			if base.Window != nil {
				windowCopy := *base.Window
				item.Window = &windowCopy
			}
			tt.mutate(&item)
			if err := ValidateMaintenanceWindowEvidence(item); !errors.Is(err, ErrInvalidMaintenanceWindowEvidence) {
				t.Fatalf("error = %v, want ErrInvalidMaintenanceWindowEvidence", err)
			}
		})
	}
}
