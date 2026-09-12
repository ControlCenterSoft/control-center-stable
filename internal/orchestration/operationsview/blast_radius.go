package operationsview

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	BlastRadiusContractVersion     = "ui.operations-blast-radius/v1"
	MaxBlastRadiusResources        = 256
	MaxBlastRadiusIdentifierLength = 255
	MaxBlastRadiusKindLength       = 128
	MaxBlastRadiusReasonCodeLength = 128
)

var ErrInvalidBlastRadius = errors.New("invalid operations blast radius")

type BlastRadiusRelation string

const (
	BlastRadiusDirect     BlastRadiusRelation = "direct"
	BlastRadiusDependency BlastRadiusRelation = "dependency"
)

// BlastRadiusResource is intentionally value-free. It identifies an affected
// resource and why it belongs to the bounded blast-radius set without exposing
// configuration values, credentials, provider payloads or operator-authored
// free text.
type BlastRadiusResource struct {
	ResourceID string              `json:"resource_id"`
	Kind       string              `json:"kind"`
	Relation   BlastRadiusRelation `json:"relation"`
	ReasonCode string              `json:"reason_code"`
}

// BlastRadiusEvidence binds an operator-safe affected-resource set to one exact
// immutable Change revision. It is review evidence only: presence of this
// contract does not approve a Change, open a maintenance window or authorize a
// Job to run.
type BlastRadiusEvidence struct {
	ContractVersion string                `json:"contract_version"`
	ChangeID        string                `json:"change_id"`
	RevisionID      string                `json:"revision_id"`
	RevisionDigest  string                `json:"revision_digest"`
	ObservedAt      time.Time             `json:"observed_at"`
	ResourceCount   int                   `json:"resource_count"`
	DirectCount     int                   `json:"direct_count"`
	DependencyCount int                   `json:"dependency_count"`
	Resources       []BlastRadiusResource `json:"resources"`
}

type BlastRadiusInput struct {
	ChangeID       string
	RevisionID     string
	RevisionDigest string
	ObservedAt     time.Time
	Resources      []BlastRadiusResource
}

// BuildBlastRadiusEvidence validates and canonicalizes authoritative
// blast-radius input. The function never infers affected resources from a
// semantic diff: dependency impact must come from an authoritative planner or
// provider. Oversized input is rejected instead of silently truncating it.
func BuildBlastRadiusEvidence(input BlastRadiusInput) (BlastRadiusEvidence, error) {
	evidence := BlastRadiusEvidence{
		ContractVersion: BlastRadiusContractVersion,
		ChangeID:        input.ChangeID,
		RevisionID:      input.RevisionID,
		RevisionDigest:  input.RevisionDigest,
		ObservedAt:      input.ObservedAt.UTC(),
		Resources:       append([]BlastRadiusResource(nil), input.Resources...),
	}
	if len(evidence.Resources) > MaxBlastRadiusResources {
		return BlastRadiusEvidence{}, fmt.Errorf("%w: resource set exceeds %d entries", ErrInvalidBlastRadius, MaxBlastRadiusResources)
	}
	sort.Slice(evidence.Resources, func(i, j int) bool {
		left, right := evidence.Resources[i], evidence.Resources[j]
		if left.ResourceID != right.ResourceID {
			return left.ResourceID < right.ResourceID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Relation != right.Relation {
			return left.Relation < right.Relation
		}
		return left.ReasonCode < right.ReasonCode
	})
	for _, resource := range evidence.Resources {
		switch resource.Relation {
		case BlastRadiusDirect:
			evidence.DirectCount++
		case BlastRadiusDependency:
			evidence.DependencyCount++
		}
	}
	evidence.ResourceCount = len(evidence.Resources)
	if err := ValidateBlastRadiusEvidence(evidence); err != nil {
		return BlastRadiusEvidence{}, err
	}
	return evidence, nil
}

// ValidateBlastRadiusEvidence is suitable for validating persisted/provider
// evidence before it is exposed by an operator-facing read model.
func ValidateBlastRadiusEvidence(evidence BlastRadiusEvidence) error {
	if evidence.ContractVersion != BlastRadiusContractVersion {
		return fmt.Errorf("%w: unexpected contract version", ErrInvalidBlastRadius)
	}
	if !validBlastRadiusIdentifier(evidence.ChangeID, MaxBlastRadiusIdentifierLength) {
		return fmt.Errorf("%w: canonical change id is required", ErrInvalidBlastRadius)
	}
	if !validBlastRadiusIdentifier(evidence.RevisionID, MaxBlastRadiusIdentifierLength) {
		return fmt.Errorf("%w: canonical revision id is required", ErrInvalidBlastRadius)
	}
	if !validBlastRadiusDigest(evidence.RevisionDigest) {
		return fmt.Errorf("%w: canonical sha256 revision digest is required", ErrInvalidBlastRadius)
	}
	if evidence.ObservedAt.IsZero() || evidence.ObservedAt.Location() != time.UTC {
		return fmt.Errorf("%w: observed_at must be a non-zero UTC timestamp", ErrInvalidBlastRadius)
	}
	if evidence.ResourceCount != len(evidence.Resources) || evidence.ResourceCount > MaxBlastRadiusResources {
		return fmt.Errorf("%w: resource count is inconsistent", ErrInvalidBlastRadius)
	}

	seen := make(map[string]struct{}, len(evidence.Resources))
	directCount := 0
	dependencyCount := 0
	for index, resource := range evidence.Resources {
		if !validBlastRadiusIdentifier(resource.ResourceID, MaxBlastRadiusIdentifierLength) {
			return fmt.Errorf("%w: resource %d has invalid resource_id", ErrInvalidBlastRadius, index)
		}
		if !validBlastRadiusIdentifier(resource.Kind, MaxBlastRadiusKindLength) {
			return fmt.Errorf("%w: resource %q has invalid kind", ErrInvalidBlastRadius, resource.ResourceID)
		}
		if !validBlastRadiusReasonCode(resource.ReasonCode) {
			return fmt.Errorf("%w: resource %q has invalid reason_code", ErrInvalidBlastRadius, resource.ResourceID)
		}
		switch resource.Relation {
		case BlastRadiusDirect:
			directCount++
		case BlastRadiusDependency:
			dependencyCount++
		default:
			return fmt.Errorf("%w: resource %q has invalid relation", ErrInvalidBlastRadius, resource.ResourceID)
		}
		key := resource.ResourceID + "\x00" + resource.Kind
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate resource %q kind %q", ErrInvalidBlastRadius, resource.ResourceID, resource.Kind)
		}
		seen[key] = struct{}{}
		if index > 0 && blastRadiusResourceLess(resource, evidence.Resources[index-1]) {
			return fmt.Errorf("%w: resources are not in canonical order", ErrInvalidBlastRadius)
		}
	}
	if evidence.DirectCount != directCount || evidence.DependencyCount != dependencyCount || directCount+dependencyCount != evidence.ResourceCount {
		return fmt.Errorf("%w: relation counts are inconsistent", ErrInvalidBlastRadius)
	}
	return nil
}

func blastRadiusResourceLess(left, right BlastRadiusResource) bool {
	if left.ResourceID != right.ResourceID {
		return left.ResourceID < right.ResourceID
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	if left.Relation != right.Relation {
		return left.Relation < right.Relation
	}
	return left.ReasonCode < right.ReasonCode
}

func validBlastRadiusIdentifier(value string, maxLength int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && value == trimmed && len(value) <= maxLength
}

func validBlastRadiusReasonCode(value string) bool {
	if value == "" || len(value) > MaxBlastRadiusReasonCodeLength {
		return false
	}
	for index, ch := range value {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-'
		if !valid || (index == 0 && (ch == '.' || ch == '_' || ch == '-')) {
			return false
		}
	}
	return true
}

func validBlastRadiusDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
