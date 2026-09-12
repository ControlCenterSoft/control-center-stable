package operationsview

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildBlastRadiusEvidenceRejectsOversizedResourceSet(t *testing.T) {
	resources := make([]BlastRadiusResource, 0, MaxBlastRadiusResources+1)
	for index := 0; index <= MaxBlastRadiusResources; index++ {
		resources = append(resources, BlastRadiusResource{
			ResourceID: fmt.Sprintf("node-%03d", index),
			Kind:       "node",
			Relation:   BlastRadiusDependency,
			ReasonCode: "dependency.transitive",
		})
	}
	_, err := BuildBlastRadiusEvidence(BlastRadiusInput{
		ChangeID:       "change-a",
		RevisionID:     "revision-a",
		RevisionDigest: "sha256:" + strings.Repeat("1", 64),
		ObservedAt:     time.Date(2026, 9, 12, 0, 20, 0, 0, time.UTC),
		Resources:      resources,
	})
	if !errors.Is(err, ErrInvalidBlastRadius) {
		t.Fatalf("expected bounded resource-set rejection, got %v", err)
	}
}

func TestBuildBlastRadiusEvidenceDoesNotMutateProviderInputOrder(t *testing.T) {
	resources := []BlastRadiusResource{
		{ResourceID: "node-z", Kind: "node", Relation: BlastRadiusDependency, ReasonCode: "dependency.transitive"},
		{ResourceID: "node-a", Kind: "node", Relation: BlastRadiusDirect, ReasonCode: "change.target"},
	}
	_, err := BuildBlastRadiusEvidence(BlastRadiusInput{
		ChangeID:       "change-a",
		RevisionID:     "revision-a",
		RevisionDigest: "sha256:" + strings.Repeat("2", 64),
		ObservedAt:     time.Date(2026, 9, 12, 0, 20, 0, 0, time.UTC),
		Resources:      resources,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resources[0].ResourceID != "node-z" || resources[1].ResourceID != "node-a" {
		t.Fatalf("provider input was mutated: %#v", resources)
	}
}
