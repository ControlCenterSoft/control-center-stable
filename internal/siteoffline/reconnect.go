package siteoffline

import (
	"errors"
	"fmt"
)

const ReconciliationDecisionContractV1 = "site.offline-reconciliation-decision/v1"

var ErrInvalidReconnectState = errors.New("invalid reconnect state")

// ReconciliationStatus never authorizes execution by itself. It classifies
// whether an offline intent may be submitted to the normal approved Change
// pipeline, was already reflected globally, or conflicts with global state.
type ReconciliationStatus string

const (
	ReconciliationReady            ReconciliationStatus = "ready_for_approved_change"
	ReconciliationAlreadyReflected ReconciliationStatus = "already_reflected"
	ReconciliationConflict         ReconciliationStatus = "conflict"
)

type ReconciliationReason string

const (
	ReconnectBaseMatches         ReconciliationReason = "base_revision_matches"
	ReconnectIntentAlreadyExists ReconciliationReason = "intent_already_reflected"
	ReconnectGenerationAdvanced  ReconciliationReason = "global_generation_advanced"
	ReconnectGenerationBehind    ReconciliationReason = "global_generation_behind_offline_base"
	ReconnectVersionDiverged     ReconciliationReason = "resource_version_diverged"
)

// ReconnectState is a metadata-only global observation obtained after WAN
// recovery. AppliedIntentDigest may be empty when the Global Controller has no
// content-addressed receipt for the object.
type ReconnectState struct {
	SiteID              string `json:"site_id"`
	ScopeID             string `json:"scope_id"`
	ObjectID            string `json:"object_id"`
	Generation          uint64 `json:"generation"`
	ResourceVersion     string `json:"resource_version"`
	AppliedIntentDigest string `json:"applied_intent_digest,omitempty"`
}

type RevisionMetadata struct {
	Generation      uint64 `json:"generation"`
	ResourceVersion string `json:"resource_version"`
}

// ReconciliationDecision contains deterministic conflict metadata but cannot
// mutate production. ConflictID is set only for a divergent reconnect.
type ReconciliationDecision struct {
	ContractVersion           string               `json:"contract_version"`
	DecisionID                string               `json:"decision_id"`
	QueueID                   string               `json:"queue_id"`
	Status                    ReconciliationStatus `json:"status"`
	Reason                    ReconciliationReason `json:"reason"`
	ConflictID                string               `json:"conflict_id,omitempty"`
	ConflictKey               string               `json:"conflict_key"`
	Expected                  RevisionMetadata     `json:"expected"`
	Observed                  RevisionMetadata     `json:"observed"`
	RequiresApprovedChange    bool                 `json:"requires_approved_change"`
	RequiresOperatorReview    bool                 `json:"requires_operator_review"`
	ProductionMutationEnabled bool                 `json:"production_mutation_enabled"`
}

// ClassifyReconnect compares immutable queue metadata with the reconnected
// global revision. It never applies, drops, or rewrites the queued intent.
func ClassifyReconnect(entry QueueEntry, current ReconnectState) (ReconciliationDecision, error) {
	if err := validateQueueEntry(entry); err != nil {
		return ReconciliationDecision{}, err
	}
	if err := validateReconnectState(current); err != nil {
		return ReconciliationDecision{}, err
	}
	if current.SiteID != entry.SiteID || current.ScopeID != entry.ScopeID || current.ObjectID != entry.ObjectID {
		return ReconciliationDecision{}, fmt.Errorf("%w: queue and global object identity differ", ErrInvalidReconnectState)
	}

	status := ReconciliationConflict
	reason := ReconnectVersionDiverged
	if current.AppliedIntentDigest == entry.IntentDigest {
		status = ReconciliationAlreadyReflected
		reason = ReconnectIntentAlreadyExists
	} else if current.Generation == entry.BaseGeneration && current.ResourceVersion == entry.BaseResourceVersion {
		status = ReconciliationReady
		reason = ReconnectBaseMatches
	} else if current.Generation > entry.BaseGeneration {
		reason = ReconnectGenerationAdvanced
	} else if current.Generation < entry.BaseGeneration {
		reason = ReconnectGenerationBehind
	}

	fingerprint := struct {
		QueueID string               `json:"queue_id"`
		Current ReconnectState       `json:"current"`
		Status  ReconciliationStatus `json:"status"`
		Reason  ReconciliationReason `json:"reason"`
	}{entry.QueueID, current, status, reason}
	digest := digestJSON(fingerprint)
	decision := ReconciliationDecision{
		ContractVersion:           ReconciliationDecisionContractV1,
		DecisionID:                "ord-" + digest[:24],
		QueueID:                   entry.QueueID,
		Status:                    status,
		Reason:                    reason,
		ConflictKey:               entry.ConflictKey,
		Expected:                  RevisionMetadata{Generation: entry.BaseGeneration, ResourceVersion: entry.BaseResourceVersion},
		Observed:                  RevisionMetadata{Generation: current.Generation, ResourceVersion: current.ResourceVersion},
		RequiresApprovedChange:    status == ReconciliationReady,
		RequiresOperatorReview:    status == ReconciliationConflict,
		ProductionMutationEnabled: false,
	}
	if status == ReconciliationConflict {
		decision.ConflictID = "ocf-" + digest[24:48]
	}
	return decision, nil
}

func validateQueueEntry(entry QueueEntry) error {
	if entry.ContractVersion != QueueEntryContractV1 {
		return fmt.Errorf("%w: unsupported contract version", ErrInvalidQueueEntry)
	}
	for name, value := range map[string]string{
		"queue_id": entry.QueueID, "request_id": entry.RequestID, "actor_id": entry.ActorID,
		"site_id": entry.SiteID, "scope_id": entry.ScopeID, "object_id": entry.ObjectID,
		"policy_id": entry.PolicyID,
	} {
		if err := validateIdentifier(name, value); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidQueueEntry, err)
		}
	}
	if entry.Sequence == 0 || entry.BaseGeneration == 0 || entry.PolicyGeneration == 0 || entry.QueuedAt.IsZero() {
		return fmt.Errorf("%w: sequence, generations, and queued_at are required", ErrInvalidQueueEntry)
	}
	if !validOperationClass(entry.Class) {
		return fmt.Errorf("%w: unsupported operation class", ErrInvalidQueueEntry)
	}
	if err := validateAction(entry.Action); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidQueueEntry, err)
	}
	if !validDigest(entry.IntentDigest) {
		return fmt.Errorf("%w: invalid intent digest", ErrInvalidQueueEntry)
	}
	if err := validateOpaque("base_resource_version", entry.BaseResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidQueueEntry, err)
	}
	if err := validateOpaque("policy_resource_version", entry.PolicyVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidQueueEntry, err)
	}
	if entry.ConflictKey != entry.ScopeID+"/"+entry.ObjectID || entry.ConflictMode != "reject_on_divergence" {
		return fmt.Errorf("%w: invalid conflict metadata", ErrInvalidQueueEntry)
	}
	return nil
}

func validateReconnectState(current ReconnectState) error {
	for name, value := range map[string]string{
		"site_id": current.SiteID, "scope_id": current.ScopeID, "object_id": current.ObjectID,
	} {
		if err := validateIdentifier(name, value); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidReconnectState, err)
		}
	}
	if current.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidReconnectState)
	}
	if err := validateOpaque("resource_version", current.ResourceVersion); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReconnectState, err)
	}
	if current.AppliedIntentDigest != "" && !validDigest(current.AppliedIntentDigest) {
		return fmt.Errorf("%w: invalid applied intent digest", ErrInvalidReconnectState)
	}
	return nil
}
