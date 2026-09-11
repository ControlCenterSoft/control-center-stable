// Package siteoffline contains fail-closed contracts for admitting work while
// a Site Controller is disconnected from the Global Controller. It only builds
// decisions and reconciliation metadata; it never executes or persists work.
package siteoffline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	AdmissionContractV1     = "site.offline-admission/v1"
	QueueEntryContractV1    = "site.offline-reconciliation-entry/v1"
	maxOpaqueValueLength    = 256
	maxIdentifierLength     = 128
	maxDelegationsPerPolicy = 1024
)

var (
	ErrInvalidAdmissionRequest = errors.New("invalid offline admission request")
	ErrInvalidOfflinePolicy    = errors.New("invalid offline policy")
	ErrInvalidQueueEntry       = errors.New("invalid offline reconciliation entry")
)

// ConnectivityState is explicit so callers cannot accidentally enter the
// offline admission path based on an omitted boolean.
type ConnectivityState string

const (
	WANAvailable   ConnectivityState = "wan_available"
	WANUnavailable ConnectivityState = "wan_unavailable"
)

// OperationClass is intentionally closed. Arbitrary shell, network or host
// operations cannot be represented by this admission contract.
type OperationClass string

const (
	OperationUI     OperationClass = "ui"
	OperationJob    OperationClass = "job"
	OperationMarket OperationClass = "market"
)

// Effect is the fail-closed result of admission evaluation.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// ReasonCode is stable machine-readable decision metadata.
type ReasonCode string

const (
	ReasonAllowed                       ReasonCode = "explicit_delegation_matched"
	ReasonWANAvailable                  ReasonCode = "offline_admission_not_applicable"
	ReasonSiteMismatch                  ReasonCode = "site_mismatch"
	ReasonCrossScope                    ReasonCode = "cross_scope_operation"
	ReasonPolicyNotYetValid             ReasonCode = "policy_not_yet_valid"
	ReasonPolicyExpired                 ReasonCode = "policy_expired"
	ReasonPolicyGenerationMismatch      ReasonCode = "policy_generation_mismatch"
	ReasonPolicyResourceVersionMismatch ReasonCode = "policy_resource_version_mismatch"
	ReasonOperationNotDelegated         ReasonCode = "operation_not_delegated"
)

// Delegation is one exact permission. Wildcards and inherited grants are not
// supported: a Site Controller may perform only the listed action in the
// listed scope.
type Delegation struct {
	ScopeID string         `json:"scope_id"`
	Class   OperationClass `json:"class"`
	Action  string         `json:"action"`
}

// PolicySnapshot is the last trusted, bounded-lifetime delegation snapshot
// available to a Site Controller. Generation and ResourceVersion form an
// exact precondition supplied by the caller.
type PolicySnapshot struct {
	PolicyID        string       `json:"policy_id"`
	SiteID          string       `json:"site_id"`
	SiteScopeID     string       `json:"site_scope_id"`
	Generation      uint64       `json:"generation"`
	ResourceVersion string       `json:"resource_version"`
	ValidFrom       time.Time    `json:"valid_from"`
	ValidUntil      time.Time    `json:"valid_until"`
	Delegations     []Delegation `json:"delegations"`
}

// AdmissionRequest contains metadata only. IntentDigest is the lowercase
// SHA-256 of the canonical operation intent; raw payloads and credentials must
// stay outside this package.
type AdmissionRequest struct {
	RequestID                     string            `json:"request_id"`
	ActorID                       string            `json:"actor_id"`
	SiteID                        string            `json:"site_id"`
	TargetScopeID                 string            `json:"target_scope_id"`
	Connectivity                  ConnectivityState `json:"connectivity"`
	Class                         OperationClass    `json:"class"`
	Action                        string            `json:"action"`
	ObjectID                      string            `json:"object_id"`
	IntentDigest                  string            `json:"intent_digest"`
	BaseGeneration                uint64            `json:"base_generation"`
	BaseResourceVersion           string            `json:"base_resource_version"`
	QueueSequence                 uint64            `json:"queue_sequence"`
	RequestedAt                   time.Time         `json:"requested_at"`
	ExpectedPolicyGeneration      uint64            `json:"expected_policy_generation"`
	ExpectedPolicyResourceVersion string            `json:"expected_policy_resource_version"`
}

// QueueEntry is safe reconciliation metadata. It deliberately does not expose
// the operation payload, repository contents, endpoints or credentials.
type QueueEntry struct {
	ContractVersion     string         `json:"contract_version"`
	QueueID             string         `json:"queue_id"`
	Sequence            uint64         `json:"sequence"`
	RequestID           string         `json:"request_id"`
	ActorID             string         `json:"actor_id"`
	SiteID              string         `json:"site_id"`
	ScopeID             string         `json:"scope_id"`
	Class               OperationClass `json:"class"`
	Action              string         `json:"action"`
	ObjectID            string         `json:"object_id"`
	IntentDigest        string         `json:"intent_digest"`
	BaseGeneration      uint64         `json:"base_generation"`
	BaseResourceVersion string         `json:"base_resource_version"`
	PolicyID            string         `json:"policy_id"`
	PolicyGeneration    uint64         `json:"policy_generation"`
	PolicyVersion       string         `json:"policy_resource_version"`
	QueuedAt            time.Time      `json:"queued_at"`
	ConflictKey         string         `json:"conflict_key"`
	ConflictMode        string         `json:"conflict_mode"`
}

// AdmissionDecision is a side-effect-free result. ProductionMutationEnabled
// is always false in the 0.6 contract.
type AdmissionDecision struct {
	ContractVersion           string      `json:"contract_version"`
	DecisionID                string      `json:"decision_id"`
	Effect                    Effect      `json:"effect"`
	Reason                    ReasonCode  `json:"reason"`
	PolicyID                  string      `json:"policy_id"`
	PolicyGeneration          uint64      `json:"policy_generation"`
	PolicyResourceVersion     string      `json:"policy_resource_version"`
	RequiresAudit             bool        `json:"requires_audit"`
	RequiresReconnectReview   bool        `json:"requires_reconnect_review"`
	ProductionMutationEnabled bool        `json:"production_mutation_enabled"`
	QueueEntry                *QueueEntry `json:"queue_entry,omitempty"`
}

// BuildAdmissionDecision validates the request and trusted policy, then makes
// an exact-scope, exact-action decision. A denied decision is returned for all
// policy freshness and authority failures. Structural errors are returned as
// errors because no trustworthy decision fingerprint can be built from them.
func BuildAdmissionDecision(request AdmissionRequest, policy PolicySnapshot) (AdmissionDecision, error) {
	if err := validateAdmissionRequest(request); err != nil {
		return AdmissionDecision{}, err
	}
	if err := validatePolicy(policy); err != nil {
		return AdmissionDecision{}, err
	}
	request.RequestedAt = request.RequestedAt.UTC()

	reason := ReasonAllowed
	if request.Connectivity != WANUnavailable {
		reason = ReasonWANAvailable
	} else if request.SiteID != policy.SiteID {
		reason = ReasonSiteMismatch
	} else if request.TargetScopeID != policy.SiteScopeID {
		reason = ReasonCrossScope
	} else if request.RequestedAt.Before(policy.ValidFrom) {
		reason = ReasonPolicyNotYetValid
	} else if !request.RequestedAt.Before(policy.ValidUntil) {
		reason = ReasonPolicyExpired
	} else if request.ExpectedPolicyGeneration != policy.Generation {
		reason = ReasonPolicyGenerationMismatch
	} else if request.ExpectedPolicyResourceVersion != policy.ResourceVersion {
		reason = ReasonPolicyResourceVersionMismatch
	} else if !hasExactDelegation(policy.Delegations, request.TargetScopeID, request.Class, request.Action) {
		reason = ReasonOperationNotDelegated
	}

	decision := AdmissionDecision{
		ContractVersion:           AdmissionContractV1,
		Effect:                    EffectDeny,
		Reason:                    reason,
		PolicyID:                  policy.PolicyID,
		PolicyGeneration:          policy.Generation,
		PolicyResourceVersion:     policy.ResourceVersion,
		RequiresAudit:             true,
		RequiresReconnectReview:   false,
		ProductionMutationEnabled: false,
	}
	if reason == ReasonAllowed {
		entry := buildQueueEntry(request, policy)
		decision.Effect = EffectAllow
		decision.RequiresReconnectReview = true
		decision.QueueEntry = &entry
	}
	decision.DecisionID = admissionDecisionID(request, policy, reason)
	return decision, nil
}

func validateAdmissionRequest(request AdmissionRequest) error {
	for name, value := range map[string]string{
		"request_id":      request.RequestID,
		"actor_id":        request.ActorID,
		"site_id":         request.SiteID,
		"target_scope_id": request.TargetScopeID,
		"object_id":       request.ObjectID,
	} {
		if err := validateIdentifier(name, value); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAdmissionRequest, err)
		}
	}
	if !validConnectivity(request.Connectivity) {
		return fmt.Errorf("%w: unsupported connectivity %q", ErrInvalidAdmissionRequest, request.Connectivity)
	}
	if !validOperationClass(request.Class) {
		return fmt.Errorf("%w: unsupported operation class %q", ErrInvalidAdmissionRequest, request.Class)
	}
	if err := validateAction(request.Action); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAdmissionRequest, err)
	}
	if !validDigest(request.IntentDigest) {
		return fmt.Errorf("%w: intent_digest must be a lowercase SHA-256", ErrInvalidAdmissionRequest)
	}
	if request.BaseGeneration == 0 || request.QueueSequence == 0 || request.ExpectedPolicyGeneration == 0 {
		return fmt.Errorf("%w: generations and queue_sequence must be positive", ErrInvalidAdmissionRequest)
	}
	if err := validateOpaque("base_resource_version", request.BaseResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAdmissionRequest, err)
	}
	if err := validateOpaque("expected_policy_resource_version", request.ExpectedPolicyResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAdmissionRequest, err)
	}
	if request.RequestedAt.IsZero() {
		return fmt.Errorf("%w: requested_at is required", ErrInvalidAdmissionRequest)
	}
	return nil
}

func validatePolicy(policy PolicySnapshot) error {
	for name, value := range map[string]string{
		"policy_id":     policy.PolicyID,
		"site_id":       policy.SiteID,
		"site_scope_id": policy.SiteScopeID,
	} {
		if err := validateIdentifier(name, value); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidOfflinePolicy, err)
		}
	}
	if policy.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidOfflinePolicy)
	}
	if err := validateOpaque("resource_version", policy.ResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOfflinePolicy, err)
	}
	if policy.ValidFrom.IsZero() || policy.ValidUntil.IsZero() || !policy.ValidUntil.After(policy.ValidFrom) {
		return fmt.Errorf("%w: finite valid_from/valid_until window is required", ErrInvalidOfflinePolicy)
	}
	if len(policy.Delegations) > maxDelegationsPerPolicy {
		return fmt.Errorf("%w: delegation count exceeds %d", ErrInvalidOfflinePolicy, maxDelegationsPerPolicy)
	}
	seen := make(map[string]struct{}, len(policy.Delegations))
	for _, delegation := range policy.Delegations {
		if err := validateIdentifier("delegation.scope_id", delegation.ScopeID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidOfflinePolicy, err)
		}
		if delegation.ScopeID != policy.SiteScopeID {
			return fmt.Errorf("%w: delegation scope %q is outside the site scope", ErrInvalidOfflinePolicy, delegation.ScopeID)
		}
		if !validOperationClass(delegation.Class) {
			return fmt.Errorf("%w: unsupported delegation class %q", ErrInvalidOfflinePolicy, delegation.Class)
		}
		if err := validateAction(delegation.Action); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidOfflinePolicy, err)
		}
		key := delegationKey(delegation.ScopeID, delegation.Class, delegation.Action)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate delegation", ErrInvalidOfflinePolicy)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func buildQueueEntry(request AdmissionRequest, policy PolicySnapshot) QueueEntry {
	fingerprint := struct {
		Request AdmissionRequest `json:"request"`
		Policy  struct {
			ID         string `json:"id"`
			Generation uint64 `json:"generation"`
			Version    string `json:"version"`
		} `json:"policy"`
	}{Request: request}
	fingerprint.Policy.ID = policy.PolicyID
	fingerprint.Policy.Generation = policy.Generation
	fingerprint.Policy.Version = policy.ResourceVersion
	queueID := "oq-" + digestJSON(fingerprint)[:24]
	return QueueEntry{
		ContractVersion:     QueueEntryContractV1,
		QueueID:             queueID,
		Sequence:            request.QueueSequence,
		RequestID:           request.RequestID,
		ActorID:             request.ActorID,
		SiteID:              request.SiteID,
		ScopeID:             request.TargetScopeID,
		Class:               request.Class,
		Action:              request.Action,
		ObjectID:            request.ObjectID,
		IntentDigest:        request.IntentDigest,
		BaseGeneration:      request.BaseGeneration,
		BaseResourceVersion: request.BaseResourceVersion,
		PolicyID:            policy.PolicyID,
		PolicyGeneration:    policy.Generation,
		PolicyVersion:       policy.ResourceVersion,
		QueuedAt:            request.RequestedAt.UTC(),
		ConflictKey:         request.TargetScopeID + "/" + request.ObjectID,
		ConflictMode:        "reject_on_divergence",
	}
}

func admissionDecisionID(request AdmissionRequest, policy PolicySnapshot, reason ReasonCode) string {
	delegations := append([]Delegation(nil), policy.Delegations...)
	sort.Slice(delegations, func(i, j int) bool {
		return delegationKey(delegations[i].ScopeID, delegations[i].Class, delegations[i].Action) <
			delegationKey(delegations[j].ScopeID, delegations[j].Class, delegations[j].Action)
	})
	fingerprint := struct {
		Request     AdmissionRequest `json:"request"`
		PolicyID    string           `json:"policy_id"`
		Generation  uint64           `json:"generation"`
		Version     string           `json:"version"`
		ValidFrom   time.Time        `json:"valid_from"`
		ValidUntil  time.Time        `json:"valid_until"`
		Delegations []Delegation     `json:"delegations"`
		Reason      ReasonCode       `json:"reason"`
	}{request, policy.PolicyID, policy.Generation, policy.ResourceVersion, policy.ValidFrom.UTC(), policy.ValidUntil.UTC(), delegations, reason}
	return "oad-" + digestJSON(fingerprint)[:24]
}

func hasExactDelegation(delegations []Delegation, scopeID string, class OperationClass, action string) bool {
	for _, delegation := range delegations {
		if delegation.ScopeID == scopeID && delegation.Class == class && delegation.Action == action {
			return true
		}
	}
	return false
}

func validConnectivity(value ConnectivityState) bool {
	return value == WANAvailable || value == WANUnavailable
}

func validOperationClass(value OperationClass) bool {
	switch value {
	case OperationUI, OperationJob, OperationMarket:
		return true
	default:
		return false
	}
}

func delegationKey(scopeID string, class OperationClass, action string) string {
	return scopeID + "\x00" + string(class) + "\x00" + action
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("siteoffline: canonical value cannot be encoded: %v", err))
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateIdentifier(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maxIdentifierLength {
		return fmt.Errorf("%s is required, bounded, and must not have surrounding whitespace", name)
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:/-", char)) {
			return fmt.Errorf("%s contains unsupported characters", name)
		}
	}
	return nil
}

func validateAction(value string) error {
	if err := validateIdentifier("action", value); err != nil {
		return err
	}
	if strings.ContainsAny(value, "*?") {
		return errors.New("action wildcards are not supported")
	}
	return nil
}

func validateOpaque(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maxOpaqueValueLength {
		return fmt.Errorf("%s is required, bounded, and must not have surrounding whitespace", name)
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
