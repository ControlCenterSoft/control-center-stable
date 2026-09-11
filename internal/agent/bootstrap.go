package agent

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	BootstrapContractV1  = "agent.bootstrap-token/v1"
	bootstrapIDBytes     = 16
	bootstrapSecretBytes = 32
	minimumBootstrapTTL  = time.Minute
	maximumBootstrapTTL  = 15 * time.Minute
)

var (
	ErrInvalidBootstrap  = errors.New("invalid bootstrap token request")
	ErrBootstrapRejected = errors.New("bootstrap token rejected")
)

type BootstrapTransport string

const (
	BootstrapTransportSSH           BootstrapTransport = "ssh"
	BootstrapTransportWinRM         BootstrapTransport = "winrm"
	BootstrapTransportOfflineBundle BootstrapTransport = "offline-bundle"
)

func (transport BootstrapTransport) Valid() bool {
	switch transport {
	case BootstrapTransportSSH, BootstrapTransportWinRM, BootstrapTransportOfflineBundle:
		return true
	default:
		return false
	}
}

type BootstrapTokenRequest struct {
	NodeID    string             `json:"node_id"`
	ScopeID   string             `json:"scope_id"`
	Transport BootstrapTransport `json:"transport"`
	TTL       time.Duration      `json:"-"`
}

type BootstrapTokenReceipt struct {
	ContractVersion string             `json:"contract_version"`
	TokenID         string             `json:"token_id"`
	NodeID          string             `json:"node_id"`
	ScopeID         string             `json:"scope_id"`
	Transport       BootstrapTransport `json:"transport"`
	IssuedAt        time.Time          `json:"issued_at"`
	ExpiresAt       time.Time          `json:"expires_at"`
}

// IssuedBootstrapToken exposes the secret exactly once. Its String method is
// deliberately receipt-only so normal structured logs cannot print the token.
type IssuedBootstrapToken struct {
	Receipt BootstrapTokenReceipt
	mu      sync.Mutex
	secret  string
}

func (issued *IssuedBootstrapToken) Reveal() (string, error) {
	if issued == nil {
		return "", ErrBootstrapRejected
	}
	issued.mu.Lock()
	defer issued.mu.Unlock()
	if issued.secret == "" {
		return "", ErrBootstrapRejected
	}
	secret := issued.secret
	issued.secret = ""
	return secret, nil
}

func (issued *IssuedBootstrapToken) String() string {
	if issued == nil {
		return "bootstrap-token{nil}"
	}
	return fmt.Sprintf(
		"bootstrap-token{id=%s,node=%s,scope=%s,transport=%s,expires=%s}",
		issued.Receipt.TokenID,
		issued.Receipt.NodeID,
		issued.Receipt.ScopeID,
		issued.Receipt.Transport,
		issued.Receipt.ExpiresAt.Format(time.RFC3339),
	)
}

type BootstrapGrant struct {
	TokenID    string             `json:"token_id"`
	NodeID     string             `json:"node_id"`
	ScopeID    string             `json:"scope_id"`
	Transport  BootstrapTransport `json:"transport"`
	ConsumedAt time.Time          `json:"consumed_at"`
}

type bootstrapTokenState struct {
	receipt  BootstrapTokenReceipt
	digest   [sha256.Size]byte
	consumed bool
}

// MemoryBootstrapTokens is a bounded-process token registry. Enrollment state
// is not persisted here; a later PostgreSQL admission path consumes the grant
// through the normal Change/Job/Audit pipeline.
type MemoryBootstrapTokens struct {
	mu     sync.Mutex
	tokens map[string]bootstrapTokenState
}

func NewMemoryBootstrapTokens() *MemoryBootstrapTokens {
	return &MemoryBootstrapTokens{tokens: make(map[string]bootstrapTokenState)}
}

func (registry *MemoryBootstrapTokens) Issue(
	request BootstrapTokenRequest,
	now time.Time,
	entropy io.Reader,
) (*IssuedBootstrapToken, error) {
	if registry == nil || entropy == nil {
		return nil, invalidBootstrap("registry and entropy are required")
	}
	request.NodeID = strings.TrimSpace(request.NodeID)
	request.ScopeID = strings.TrimSpace(request.ScopeID)
	if err := validateIdentifier("node_id", request.NodeID, 128); err != nil {
		return nil, invalidBootstrap("node_id is not a canonical identifier")
	}
	if err := validateIdentifier("scope_id", request.ScopeID, 128); err != nil {
		return nil, invalidBootstrap("scope_id is not a canonical identifier")
	}
	if !request.Transport.Valid() {
		return nil, invalidBootstrap("transport is unsupported")
	}
	if request.TTL < minimumBootstrapTTL || request.TTL > maximumBootstrapTTL {
		return nil, invalidBootstrap("ttl must be between one and fifteen minutes")
	}
	if now.IsZero() {
		return nil, invalidBootstrap("current time is required")
	}

	material := make([]byte, bootstrapIDBytes+bootstrapSecretBytes)
	if _, err := io.ReadFull(entropy, material); err != nil {
		return nil, invalidBootstrap("secure entropy is unavailable")
	}
	tokenID := base64.RawURLEncoding.EncodeToString(material[:bootstrapIDBytes])
	secret := base64.RawURLEncoding.EncodeToString(material[bootstrapIDBytes:])
	digest := sha256.Sum256([]byte(secret))
	receipt := BootstrapTokenReceipt{
		ContractVersion: BootstrapContractV1,
		TokenID:         tokenID,
		NodeID:          request.NodeID,
		ScopeID:         request.ScopeID,
		Transport:       request.Transport,
		IssuedAt:        now.UTC(),
		ExpiresAt:       now.UTC().Add(request.TTL),
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.tokens[tokenID]; exists {
		return nil, invalidBootstrap("token identifier collision")
	}
	registry.tokens[tokenID] = bootstrapTokenState{receipt: receipt, digest: digest}
	return &IssuedBootstrapToken{Receipt: receipt, secret: secret}, nil
}

func (registry *MemoryBootstrapTokens) Consume(
	tokenID string,
	presentedSecret string,
	now time.Time,
) (BootstrapGrant, error) {
	if registry == nil || now.IsZero() {
		return BootstrapGrant{}, ErrBootstrapRejected
	}
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" || presentedSecret == "" {
		return BootstrapGrant{}, ErrBootstrapRejected
	}
	presentedDigest := sha256.Sum256([]byte(presentedSecret))

	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, exists := registry.tokens[tokenID]
	if !exists || state.consumed || !now.UTC().Before(state.receipt.ExpiresAt) {
		return BootstrapGrant{}, ErrBootstrapRejected
	}
	if subtle.ConstantTimeCompare(presentedDigest[:], state.digest[:]) != 1 {
		return BootstrapGrant{}, ErrBootstrapRejected
	}
	state.consumed = true
	state.digest = [sha256.Size]byte{}
	registry.tokens[tokenID] = state
	return BootstrapGrant{
		TokenID:    state.receipt.TokenID,
		NodeID:     state.receipt.NodeID,
		ScopeID:    state.receipt.ScopeID,
		Transport:  state.receipt.Transport,
		ConsumedAt: now.UTC(),
	}, nil
}

func invalidBootstrap(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidBootstrap, message)
}
