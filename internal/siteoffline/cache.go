package siteoffline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	RepositoryCacheContractV1        = "site.repository-cache-metadata/v1"
	MaxCacheMetadataBytes            = 1 << 20
	MaxCacheEntries                  = 1024
	MaxCacheEntryBytes        uint64 = 4 << 30
	MaxCacheTotalBytes        uint64 = 32 << 30
)

var ErrInvalidCacheMetadata = errors.New("invalid site repository cache metadata")

// CacheEntry contains identity, integrity, size and lifetime metadata only.
// Package bytes, file paths, remote URLs, access tokens and credentials are
// deliberately absent from the contract.
type CacheEntry struct {
	ArtifactID string    `json:"artifact_id"`
	ModuleID   string    `json:"module_id"`
	Version    string    `json:"version"`
	SHA256     string    `json:"sha256"`
	SizeBytes  uint64    `json:"size_bytes"`
	CachedAt   time.Time `json:"cached_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// RepositoryCacheMetadata is a bounded inventory of locally cached market
// artifacts. MetadataGeneration is local monotonic metadata, while
// PolicyGeneration binds it to the policy that allowed caching.
type RepositoryCacheMetadata struct {
	ContractVersion    string       `json:"contract_version"`
	SiteID             string       `json:"site_id"`
	ScopeID            string       `json:"scope_id"`
	MetadataGeneration uint64       `json:"metadata_generation"`
	PolicyGeneration   uint64       `json:"policy_generation"`
	UpdatedAt          time.Time    `json:"updated_at"`
	Entries            []CacheEntry `json:"entries"`
}

// DecodeRepositoryCacheMetadata rejects unknown fields and trailing JSON.
// Consequently callers cannot smuggle repository contents or credentials into
// this metadata-only contract.
func DecodeRepositoryCacheMetadata(reader io.Reader) (RepositoryCacheMetadata, error) {
	if reader == nil {
		return RepositoryCacheMetadata{}, fmt.Errorf("%w: reader is required", ErrInvalidCacheMetadata)
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, MaxCacheMetadataBytes+1))
	if err != nil {
		return RepositoryCacheMetadata{}, fmt.Errorf("%w: read: %v", ErrInvalidCacheMetadata, err)
	}
	if len(encoded) == 0 || len(encoded) > MaxCacheMetadataBytes {
		return RepositoryCacheMetadata{}, fmt.Errorf("%w: document exceeds %d bytes or is empty", ErrInvalidCacheMetadata, MaxCacheMetadataBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var metadata RepositoryCacheMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return RepositoryCacheMetadata{}, fmt.Errorf("%w: decode: %v", ErrInvalidCacheMetadata, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return RepositoryCacheMetadata{}, fmt.Errorf("%w: exactly one JSON document is required", ErrInvalidCacheMetadata)
	}
	if err := metadata.Validate(); err != nil {
		return RepositoryCacheMetadata{}, err
	}
	return metadata, nil
}

func (metadata RepositoryCacheMetadata) Validate() error {
	if metadata.ContractVersion != RepositoryCacheContractV1 {
		return fmt.Errorf("%w: unsupported contract version", ErrInvalidCacheMetadata)
	}
	if err := validateIdentifier("site_id", metadata.SiteID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCacheMetadata, err)
	}
	if err := validateIdentifier("scope_id", metadata.ScopeID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCacheMetadata, err)
	}
	if metadata.MetadataGeneration == 0 || metadata.PolicyGeneration == 0 || metadata.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: generations and updated_at are required", ErrInvalidCacheMetadata)
	}
	if len(metadata.Entries) > MaxCacheEntries {
		return fmt.Errorf("%w: entry count exceeds %d", ErrInvalidCacheMetadata, MaxCacheEntries)
	}
	seen := make(map[string]struct{}, len(metadata.Entries))
	var total uint64
	for _, entry := range metadata.Entries {
		if err := validateIdentifier("artifact_id", entry.ArtifactID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCacheMetadata, err)
		}
		if err := validateIdentifier("module_id", entry.ModuleID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCacheMetadata, err)
		}
		if err := validateOpaque("version", entry.Version); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCacheMetadata, err)
		}
		if !validDigest(entry.SHA256) {
			return fmt.Errorf("%w: artifact %q has invalid sha256", ErrInvalidCacheMetadata, entry.ArtifactID)
		}
		if entry.SizeBytes == 0 || entry.SizeBytes > MaxCacheEntryBytes || entry.SizeBytes > MaxCacheTotalBytes-total {
			return fmt.Errorf("%w: artifact %q exceeds cache size bounds", ErrInvalidCacheMetadata, entry.ArtifactID)
		}
		if entry.CachedAt.IsZero() || entry.ExpiresAt.IsZero() || !entry.ExpiresAt.After(entry.CachedAt) || entry.CachedAt.After(metadata.UpdatedAt) {
			return fmt.Errorf("%w: artifact %q has invalid timestamps", ErrInvalidCacheMetadata, entry.ArtifactID)
		}
		if _, duplicate := seen[entry.ArtifactID]; duplicate {
			return fmt.Errorf("%w: duplicate artifact %q", ErrInvalidCacheMetadata, entry.ArtifactID)
		}
		seen[entry.ArtifactID] = struct{}{}
		total += entry.SizeBytes
	}
	return nil
}
