package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrEmptyContent         = errors.New("configuration content is empty")
	ErrPreconditionRequired = errors.New("revision precondition is required")
	ErrPreconditionFailed   = errors.New("revision precondition failed")
)

// Revision is an immutable, content-addressed configuration snapshot.
// Its payload is copied both on construction and access.
type Revision struct {
	id        string
	sequence  uint64
	createdAt time.Time
	content   []byte
	digest    string
}

func NewRevision(id string, sequence uint64, createdAt time.Time, content []byte) (Revision, error) {
	if id == "" {
		return Revision{}, errors.New("revision id is required")
	}
	if sequence == 0 {
		return Revision{}, errors.New("revision sequence must be positive")
	}
	if len(content) == 0 {
		return Revision{}, ErrEmptyContent
	}
	if createdAt.IsZero() {
		return Revision{}, errors.New("revision creation time is required")
	}
	copyOfContent := append([]byte(nil), content...)
	sum := sha256.Sum256(copyOfContent)
	return Revision{
		id:        id,
		sequence:  sequence,
		createdAt: createdAt.UTC(),
		content:   copyOfContent,
		digest:    "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func (r Revision) ID() string           { return r.id }
func (r Revision) Sequence() uint64     { return r.sequence }
func (r Revision) CreatedAt() time.Time { return r.createdAt }
func (r Revision) Digest() string       { return r.digest }
func (r Revision) Content() []byte      { return append([]byte(nil), r.content...) }

// Precondition protects changes from being applied to a newer configuration.
type Precondition struct {
	ExpectedRevisionID string `json:"expectedRevisionId"`
	ExpectedDigest     string `json:"expectedDigest,omitempty"`
}

func (p Precondition) ValidateAgainst(current Revision) error {
	if p.ExpectedRevisionID == "" {
		return ErrPreconditionRequired
	}
	if p.ExpectedRevisionID != current.ID() {
		return fmt.Errorf("%w: expected revision %q, current %q", ErrPreconditionFailed, p.ExpectedRevisionID, current.ID())
	}
	if p.ExpectedDigest != "" && p.ExpectedDigest != current.Digest() {
		return fmt.Errorf("%w: expected digest %q, current %q", ErrPreconditionFailed, p.ExpectedDigest, current.Digest())
	}
	return nil
}
