package agent

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBootstrapTokenIsOneTimeAndBoundToNodeScopeAndTransport(t *testing.T) {
	registry := NewMemoryBootstrapTokens()
	now := time.Date(2026, 9, 9, 6, 0, 0, 0, time.FixedZone("operator", 3*60*60))
	issued, err := registry.Issue(BootstrapTokenRequest{
		NodeID: "node-a", ScopeID: "site-a", Transport: BootstrapTransportSSH, TTL: 10 * time.Minute,
	}, now, bytes.NewReader(bytes.Repeat([]byte{0x42}, bootstrapIDBytes+bootstrapSecretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if issued.Receipt.ContractVersion != BootstrapContractV1 || issued.Receipt.IssuedAt.Location() != time.UTC {
		t.Fatalf("receipt = %#v", issued.Receipt)
	}
	secret, err := issued.Reveal()
	if err != nil || secret == "" {
		t.Fatalf("Reveal() secret=%q err=%v", secret, err)
	}
	if _, err := issued.Reveal(); !errors.Is(err, ErrBootstrapRejected) {
		t.Fatalf("second Reveal() error = %v", err)
	}
	grant, err := registry.Consume(issued.Receipt.TokenID, secret, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if grant.NodeID != "node-a" || grant.ScopeID != "site-a" || grant.Transport != BootstrapTransportSSH {
		t.Fatalf("grant = %#v", grant)
	}
	if _, err := registry.Consume(issued.Receipt.TokenID, secret, now.Add(2*time.Minute)); !errors.Is(err, ErrBootstrapRejected) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestBootstrapSecretIsNotRenderedByStringer(t *testing.T) {
	registry := NewMemoryBootstrapTokens()
	issued, err := registry.Issue(BootstrapTokenRequest{
		NodeID: "node-a", ScopeID: "global", Transport: BootstrapTransportOfflineBundle, TTL: time.Minute,
	}, time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC), bytes.NewReader(bytes.Repeat([]byte{0x24}, bootstrapIDBytes+bootstrapSecretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := issued.Reveal()
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprint(issued)
	if strings.Contains(rendered, secret) || !strings.Contains(rendered, issued.Receipt.TokenID) {
		t.Fatalf("unsafe String() output %q", rendered)
	}
}

func TestBootstrapTokenExpiresAndWrongSecretIsRejected(t *testing.T) {
	registry := NewMemoryBootstrapTokens()
	now := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	issued, err := registry.Issue(BootstrapTokenRequest{
		NodeID: "node-a", ScopeID: "global", Transport: BootstrapTransportWinRM, TTL: time.Minute,
	}, now, bytes.NewReader(bytes.Repeat([]byte{0x61}, bootstrapIDBytes+bootstrapSecretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := issued.Reveal()
	if _, err := registry.Consume(issued.Receipt.TokenID, "wrong-secret", now.Add(30*time.Second)); !errors.Is(err, ErrBootstrapRejected) {
		t.Fatalf("wrong secret error = %v", err)
	}
	if _, err := registry.Consume(issued.Receipt.TokenID, secret, issued.Receipt.ExpiresAt); !errors.Is(err, ErrBootstrapRejected) {
		t.Fatalf("expired token error = %v", err)
	}
}

func TestBootstrapIssueFailsClosed(t *testing.T) {
	registry := NewMemoryBootstrapTokens()
	now := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	tests := []BootstrapTokenRequest{
		{NodeID: "", ScopeID: "global", Transport: BootstrapTransportSSH, TTL: time.Minute},
		{NodeID: "node-a", ScopeID: "bad scope", Transport: BootstrapTransportSSH, TTL: time.Minute},
		{NodeID: "node-a", ScopeID: "global", Transport: "shell", TTL: time.Minute},
		{NodeID: "node-a", ScopeID: "global", Transport: BootstrapTransportSSH, TTL: time.Second},
		{NodeID: "node-a", ScopeID: "global", Transport: BootstrapTransportSSH, TTL: 16 * time.Minute},
	}
	for _, request := range tests {
		if _, err := registry.Issue(request, now, bytes.NewReader(make([]byte, 64))); !errors.Is(err, ErrInvalidBootstrap) {
			t.Fatalf("request=%#v error=%v", request, err)
		}
	}
}

func TestBootstrapConsumeIsAtomicUnderReplay(t *testing.T) {
	registry := NewMemoryBootstrapTokens()
	now := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	issued, err := registry.Issue(BootstrapTokenRequest{
		NodeID: "node-a", ScopeID: "global", Transport: BootstrapTransportSSH, TTL: 5 * time.Minute,
	}, now, bytes.NewReader(bytes.Repeat([]byte{0x19}, bootstrapIDBytes+bootstrapSecretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := issued.Reveal()
	const consumers = 32
	results := make(chan error, consumers)
	var group sync.WaitGroup
	for index := 0; index < consumers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, consumeErr := registry.Consume(issued.Receipt.TokenID, secret, now.Add(time.Minute))
			results <- consumeErr
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for consumeErr := range results {
		if consumeErr == nil {
			successes++
		} else if !errors.Is(consumeErr, ErrBootstrapRejected) {
			t.Fatalf("unexpected error = %v", consumeErr)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumes = %d, want 1", successes)
	}
}
