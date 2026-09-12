package operationsview

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildBlastRadiusEvidenceCanonicalizesAndCounts(t *testing.T) {
	observedAt := time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC)
	evidence, err := BuildBlastRadiusEvidence(BlastRadiusInput{
		ChangeID:       "change-031-a",
		RevisionID:     "revision-031-a",
		RevisionDigest: "sha256:" + strings.Repeat("a", 64),
		ObservedAt:     observedAt,
		Resources: []BlastRadiusResource{
			{ResourceID: "node-b", Kind: "node", Relation: BlastRadiusDependency, ReasonCode: "service.depends-on-node"},
			{ResourceID: "service-a", Kind: "service", Relation: BlastRadiusDirect, ReasonCode: "change.target"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ContractVersion != BlastRadiusContractVersion || evidence.ResourceCount != 2 || evidence.DirectCount != 1 || evidence.DependencyCount != 1 {
		t.Fatalf("unexpected blast radius summary: %#v", evidence)
	}
	if len(evidence.Resources) != 2 || evidence.Resources[0].ResourceID != "node-b" || evidence.Resources[1].ResourceID != "service-a" {
		t.Fatalf("resources are not canonically sorted: %#v", evidence.Resources)
	}
	if err := ValidateBlastRadiusEvidence(evidence); err != nil {
		t.Fatalf("built evidence must validate: %v", err)
	}
}

func TestBuildBlastRadiusEvidenceRejectsDuplicateResourceIdentity(t *testing.T) {
	_, err := BuildBlastRadiusEvidence(BlastRadiusInput{
		ChangeID:       "change-031-a",
		RevisionID:     "revision-031-a",
		RevisionDigest: "sha256:" + strings.Repeat("b", 64),
		ObservedAt:     time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC),
		Resources: []BlastRadiusResource{
			{ResourceID: "node-a", Kind: "node", Relation: BlastRadiusDirect, ReasonCode: "change.target"},
			{ResourceID: "node-a", Kind: "node", Relation: BlastRadiusDependency, ReasonCode: "dependency.transitive"},
		},
	})
	if !errors.Is(err, ErrInvalidBlastRadius) {
		t.Fatalf("expected duplicate identity rejection, got %v", err)
	}
}

func TestBuildBlastRadiusEvidenceRejectsNonCanonicalMetadata(t *testing.T) {
	tests := []struct {
		name  string
		input BlastRadiusInput
	}{
		{
			name: "digest",
			input: BlastRadiusInput{
				ChangeID: "change-a", RevisionID: "revision-a", RevisionDigest: "sha256:not-a-digest",
				ObservedAt: time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC),
			},
		},
		{
			name: "reason code",
			input: BlastRadiusInput{
				ChangeID: "change-a", RevisionID: "revision-a", RevisionDigest: "sha256:" + strings.Repeat("c", 64),
				ObservedAt: time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC),
				Resources:  []BlastRadiusResource{{ResourceID: "node-a", Kind: "node", Relation: BlastRadiusDirect, ReasonCode: "contains secret text"}},
			},
		},
		{
			name: "relation",
			input: BlastRadiusInput{
				ChangeID: "change-a", RevisionID: "revision-a", RevisionDigest: "sha256:" + strings.Repeat("d", 64),
				ObservedAt: time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC),
				Resources:  []BlastRadiusResource{{ResourceID: "node-a", Kind: "node", Relation: "guessed", ReasonCode: "dependency.unknown"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildBlastRadiusEvidence(test.input); !errors.Is(err, ErrInvalidBlastRadius) {
				t.Fatalf("expected fail-closed validation error, got %v", err)
			}
		})
	}
}

func TestValidateBlastRadiusEvidenceRejectsNonCanonicalOrder(t *testing.T) {
	evidence := BlastRadiusEvidence{
		ContractVersion: BlastRadiusContractVersion,
		ChangeID:        "change-a",
		RevisionID:      "revision-a",
		RevisionDigest:  "sha256:" + strings.Repeat("e", 64),
		ObservedAt:      time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC),
		ResourceCount:   2,
		DirectCount:     2,
		Resources: []BlastRadiusResource{
			{ResourceID: "service-z", Kind: "service", Relation: BlastRadiusDirect, ReasonCode: "change.target"},
			{ResourceID: "service-a", Kind: "service", Relation: BlastRadiusDirect, ReasonCode: "change.target"},
		},
	}
	if err := ValidateBlastRadiusEvidence(evidence); !errors.Is(err, ErrInvalidBlastRadius) {
		t.Fatalf("expected canonical-order rejection, got %v", err)
	}
}

func TestBlastRadiusEvidenceJSONContainsNoConfigurationPayload(t *testing.T) {
	evidence, err := BuildBlastRadiusEvidence(BlastRadiusInput{
		ChangeID:       "change-a",
		RevisionID:     "revision-a",
		RevisionDigest: "sha256:" + strings.Repeat("f", 64),
		ObservedAt:     time.Date(2026, 9, 12, 0, 15, 0, 0, time.UTC),
		Resources: []BlastRadiusResource{
			{ResourceID: "service-a", Kind: "service", Relation: BlastRadiusDirect, ReasonCode: "change.target"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"before_value", "after_value", "payload", "credential", "secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("blast radius contract leaked forbidden field %q: %s", forbidden, encoded)
		}
	}
}
