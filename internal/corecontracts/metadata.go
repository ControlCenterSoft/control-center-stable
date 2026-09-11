// Package corecontracts contains the versioned, side-effect-free contracts
// shared by the distributed Control Center core.
package corecontracts

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const maxIdentifierLength = 255

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$`)

var (
	// ErrInvalidMetadata identifies a malformed distributed object envelope.
	ErrInvalidMetadata = errors.New("invalid distributed object metadata")
	// ErrPreconditionRequired means an optimistic-concurrency guard is missing.
	ErrPreconditionRequired = errors.New("object precondition is required")
	// ErrInvalidPrecondition means a supplied guard is structurally malformed.
	ErrInvalidPrecondition = errors.New("invalid object precondition")
	// ErrPreconditionFailed means the guarded object has changed or is not the
	// object the caller read.
	ErrPreconditionFailed = errors.New("object precondition failed")
	// ErrInvalidTransition identifies an impossible object-version transition.
	ErrInvalidTransition = errors.New("invalid distributed object transition")
)

// ObjectMetadata is the mandatory envelope for a synchronized object.
//
// Generation is the desired semantic generation. It starts at one and changes
// only when desired content changes. ResourceVersion is an opaque token owned
// by the persistence/synchronization layer and changes on every stored update;
// callers must never parse or order it.
type ObjectMetadata struct {
	ObjectID        string    `json:"object_id"`
	ScopeID         string    `json:"scope_id"`
	OwnerScope      string    `json:"owner_scope"`
	Generation      uint64    `json:"generation"`
	ResourceVersion string    `json:"resource_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Validate checks the structural invariants that are independent of storage
// and topology.
func (m ObjectMetadata) Validate() error {
	if err := validateIdentifier("object_id", m.ObjectID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMetadata, err)
	}
	if err := validateIdentifier("scope_id", m.ScopeID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMetadata, err)
	}
	if err := validateIdentifier("owner_scope", m.OwnerScope); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMetadata, err)
	}
	if m.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidMetadata)
	}
	if err := validateOpaqueVersion(m.ResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMetadata, err)
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: created_at and updated_at are required", ErrInvalidMetadata)
	}
	if m.UpdatedAt.Before(m.CreatedAt) {
		return fmt.Errorf("%w: updated_at precedes created_at", ErrInvalidMetadata)
	}
	return nil
}

// ObjectPrecondition is the compare-and-swap guard for any object update or
// deletion. ObjectID and ResourceVersion are mandatory. Generation can also be
// supplied when a caller needs to guard the desired semantic generation.
type ObjectPrecondition struct {
	ObjectID        string  `json:"object_id"`
	ResourceVersion string  `json:"resource_version"`
	Generation      *uint64 `json:"generation,omitempty"`
}

// ValidateAgainst checks a precondition without changing either value.
func (p ObjectPrecondition) ValidateAgainst(current ObjectMetadata) error {
	if p.ObjectID == "" || p.ResourceVersion == "" {
		return ErrPreconditionRequired
	}
	if err := validateIdentifier("object_id", p.ObjectID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPrecondition, err)
	}
	if err := validateOpaqueVersion(p.ResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPrecondition, err)
	}
	if p.Generation != nil && *p.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive when supplied", ErrInvalidPrecondition)
	}
	if err := current.Validate(); err != nil {
		return err
	}
	if p.ObjectID != current.ObjectID {
		return fmt.Errorf("%w: expected object %q, current object %q", ErrPreconditionFailed, p.ObjectID, current.ObjectID)
	}
	if p.ResourceVersion != current.ResourceVersion {
		return fmt.Errorf("%w: expected resource version %q, current %q", ErrPreconditionFailed, p.ResourceVersion, current.ResourceVersion)
	}
	if p.Generation != nil && *p.Generation != current.Generation {
		return fmt.Errorf("%w: expected generation %d, current %d", ErrPreconditionFailed, *p.Generation, current.Generation)
	}
	return nil
}

// ValidateSuccessor verifies the portable part of object-version semantics.
// It does not allocate a version or persist an update. A storage adapter still
// has to perform ObjectPrecondition validation atomically with its write.
func ValidateSuccessor(current, next ObjectMetadata, desiredChanged bool) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w: current: %v", ErrInvalidTransition, err)
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: next: %v", ErrInvalidTransition, err)
	}
	if next.ObjectID != current.ObjectID {
		return fmt.Errorf("%w: object_id is immutable", ErrInvalidTransition)
	}
	if !next.CreatedAt.Equal(current.CreatedAt) {
		return fmt.Errorf("%w: created_at is immutable", ErrInvalidTransition)
	}
	if next.UpdatedAt.Before(current.UpdatedAt) {
		return fmt.Errorf("%w: updated_at moved backwards", ErrInvalidTransition)
	}
	if next.ResourceVersion == current.ResourceVersion {
		return fmt.Errorf("%w: resource_version must change on every stored update", ErrInvalidTransition)
	}
	if !desiredChanged && (next.ScopeID != current.ScopeID || next.OwnerScope != current.OwnerScope) {
		return fmt.Errorf("%w: scope ownership changes require a desired generation change", ErrInvalidTransition)
	}

	wantGeneration := current.Generation
	if desiredChanged {
		if current.Generation == math.MaxUint64 {
			return fmt.Errorf("%w: generation overflow", ErrInvalidTransition)
		}
		wantGeneration++
	}
	if next.Generation != wantGeneration {
		return fmt.Errorf("%w: generation is %d, want %d", ErrInvalidTransition, next.Generation, wantGeneration)
	}
	return nil
}

func validateIdentifier(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", field)
	}
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("%s exceeds %d bytes", field, maxIdentifierLength)
	}
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s must use letters, digits, dot, underscore, colon or hyphen and have alphanumeric ends", field)
	}
	return nil
}

func validateOpaqueVersion(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return errors.New("resource_version is required and must not have surrounding whitespace")
	}
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("resource_version exceeds %d bytes", maxIdentifierLength)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("resource_version contains whitespace or control characters")
		}
	}
	return nil
}
