package siteoffline

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validCacheMetadata() RepositoryCacheMetadata {
	return RepositoryCacheMetadata{
		ContractVersion:    RepositoryCacheContractV1,
		SiteID:             "site-a",
		ScopeID:            "scope-site-a",
		MetadataGeneration: 3,
		PolicyGeneration:   7,
		UpdatedAt:          testNow,
		Entries: []CacheEntry{{
			ArtifactID: "artifact-1",
			ModuleID:   "dns-service",
			Version:    "1.2.3",
			SHA256:     testDigest('b'),
			SizeBytes:  4096,
			CachedAt:   testNow.Add(-time.Minute),
			ExpiresAt:  testNow.Add(24 * time.Hour),
		}},
	}
}

func TestDecodeRepositoryCacheMetadataAcceptsBoundedMetadata(t *testing.T) {
	encoded, err := json.Marshal(validCacheMetadata())
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRepositoryCacheMetadata(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatalf("DecodeRepositoryCacheMetadata() error = %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].ArtifactID != "artifact-1" {
		t.Fatalf("metadata = %#v", got)
	}
}

func TestDecodeRepositoryCacheMetadataRejectsContentAndCredentials(t *testing.T) {
	encoded, err := json.Marshal(validCacheMetadata())
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(string(encoded), "}")
	for _, field := range []string{
		`,"content":"package bytes"}`,
		`,"credentials":"secret"}`,
		`,"repository_url":"https://user:password@example.invalid"}`,
	} {
		if _, err := DecodeRepositoryCacheMetadata(strings.NewReader(base + field)); !errors.Is(err, ErrInvalidCacheMetadata) {
			t.Fatalf("unknown field %s error = %v", field, err)
		}
	}
}

func TestRepositoryCacheMetadataEnforcesCountAndSizeBounds(t *testing.T) {
	metadata := validCacheMetadata()
	metadata.Entries[0].SizeBytes = MaxCacheEntryBytes + 1
	if err := metadata.Validate(); !errors.Is(err, ErrInvalidCacheMetadata) {
		t.Fatalf("oversize entry error = %v", err)
	}

	metadata = validCacheMetadata()
	metadata.Entries = make([]CacheEntry, MaxCacheEntries+1)
	if err := metadata.Validate(); !errors.Is(err, ErrInvalidCacheMetadata) {
		t.Fatalf("entry count error = %v", err)
	}

	if _, err := DecodeRepositoryCacheMetadata(strings.NewReader(strings.Repeat(" ", MaxCacheMetadataBytes+1))); !errors.Is(err, ErrInvalidCacheMetadata) {
		t.Fatalf("document size error = %v", err)
	}
}

func TestRepositoryCacheMetadataRejectsDuplicateAndInvalidLifetime(t *testing.T) {
	metadata := validCacheMetadata()
	metadata.Entries = append(metadata.Entries, metadata.Entries[0])
	if err := metadata.Validate(); !errors.Is(err, ErrInvalidCacheMetadata) {
		t.Fatalf("duplicate error = %v", err)
	}

	metadata = validCacheMetadata()
	metadata.Entries[0].ExpiresAt = metadata.Entries[0].CachedAt
	if err := metadata.Validate(); !errors.Is(err, ErrInvalidCacheMetadata) {
		t.Fatalf("lifetime error = %v", err)
	}
}

func TestDecodeRepositoryCacheMetadataRejectsTrailingDocument(t *testing.T) {
	encoded, err := json.Marshal(validCacheMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRepositoryCacheMetadata(strings.NewReader(string(encoded) + `{}`)); !errors.Is(err, ErrInvalidCacheMetadata) {
		t.Fatalf("trailing document error = %v", err)
	}
}
