package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	ErrEmptyContent         = errors.New("configuration content is empty")
	ErrPreconditionRequired = errors.New("revision precondition is required")
	ErrPreconditionFailed   = errors.New("revision precondition failed")
)

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
	copyOfContent := canonicalContent(content)
	sum := sha256.Sum256(copyOfContent)
	return Revision{id: id, sequence: sequence, createdAt: createdAt.UTC(), content: copyOfContent, digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

// canonicalContent gives JSON configuration revisions a stable byte identity
// across storage adapters. PostgreSQL jsonb normalizes whitespace and object
// key order, so hashing the request bytes directly would make a valid revision
// fail its integrity check after restart. Non-JSON configuration remains byte
// preserving for compatibility with the generic revision contract.
func canonicalContent(content []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return append([]byte(nil), content...)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return append([]byte(nil), content...)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return append([]byte(nil), content...)
	}
	return canonical
}
func (r Revision) ID() string           { return r.id }
func (r Revision) Sequence() uint64     { return r.sequence }
func (r Revision) CreatedAt() time.Time { return r.createdAt }
func (r Revision) Digest() string       { return r.digest }
func (r Revision) Content() []byte      { return append([]byte(nil), r.content...) }

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
