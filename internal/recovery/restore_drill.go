package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"control-center/internal/corecontracts"
)

const RestoreDrillPlanSchemaVersion = "recovery.restore-drill.plan/v1"

var (
	ErrInvalidRestoreDrillPlan       = errors.New("invalid restore drill plan")
	ErrInvalidRestoreDrillTransition = errors.New("invalid restore drill transition")
)

type RestoreDrillStage string

const (
	RestoreDrillPreflight    RestoreDrillStage = "PREFLIGHT"
	RestoreDrillRestore      RestoreDrillStage = "RESTORE"
	RestoreDrillVerification RestoreDrillStage = "VERIFICATION"
	RestoreDrillRecord       RestoreDrillStage = "RECORD"
)

type RestoreDrillAction string

const (
	DrillValidateMetadata    RestoreDrillAction = "VALIDATE_RECOVERY_METADATA"
	DrillResolveAdapter      RestoreDrillAction = "RESOLVE_REGISTERED_ADAPTER"
	DrillRestoreIsolatedCopy RestoreDrillAction = "RESTORE_ISOLATED_COPY"
	DrillRecordRestoreProof  RestoreDrillAction = "RECORD_RESTORE_DRILL_EVIDENCE"
	DrillRunFunctionalTests  RestoreDrillAction = "RUN_FUNCTIONAL_TESTS"
	DrillRecordVerification  RestoreDrillAction = "RECORD_VERIFICATION_RESULT"
)

// RestoreDrillStep is a closed, declarative action. There is intentionally no
// command, endpoint, environment, credential reference, or executable payload.
type RestoreDrillStep struct {
	Order                uint32               `json:"order"`
	Stage                RestoreDrillStage    `json:"stage"`
	Action               RestoreDrillAction   `json:"action"`
	RequiredCapabilities []ProviderCapability `json:"required_capabilities"`
	RequiredEvidence     []EvidenceKind       `json:"required_evidence"`
}

// RestoreDrillPlan is an immutable planning result for an already-valid
// ISOLATED_DRILL RestoreMetadata record. Building it performs no provider,
// repository, host, network, storage, or persistence operation.
type RestoreDrillPlan struct {
	SchemaVersion            string               `json:"schema_version"`
	PlanID                   string               `json:"plan_id"`
	RestoreID                string               `json:"restore_id"`
	ScopeID                  string               `json:"scope_id"`
	RecoveryPointID          string               `json:"recovery_point_id"`
	BackupIDs                []string             `json:"backup_ids"`
	Target                   ObjectReference      `json:"target"`
	Provider                 ProviderMetadata     `json:"provider"`
	Adapter                  AdapterDescriptor    `json:"adapter"`
	BasedOnResourceVersion   string               `json:"based_on_resource_version"`
	BasedOnGeneration        uint64               `json:"based_on_generation"`
	EvaluatedAt              time.Time            `json:"evaluated_at"`
	PlanOnly                 bool                 `json:"plan_only"`
	ExecutesProvider         bool                 `json:"executes_provider"`
	UsesCredentials          bool                 `json:"uses_credentials"`
	ProductionMutation       bool                 `json:"production_mutation"`
	RequiresAuditedChangeJob bool                 `json:"requires_audited_change_job"`
	RequiredCapabilities     []ProviderCapability `json:"required_capabilities"`
	Steps                    []RestoreDrillStep   `json:"steps"`
}

func BuildRestoreDrillPlan(registry *AdapterRegistry, restore RestoreMetadata, evaluatedAt time.Time) (RestoreDrillPlan, error) {
	if err := ValidateRestoreMetadata(restore); err != nil {
		return RestoreDrillPlan{}, fmt.Errorf("%w: %v", ErrInvalidRestoreDrillPlan, err)
	}
	if restore.Mode != RestoreIsolatedDrill || restore.State != RestorePlanned {
		return RestoreDrillPlan{}, fmt.Errorf("%w: restore must be a planned ISOLATED_DRILL", ErrInvalidRestoreDrillPlan)
	}
	if err := validateTimestamp("evaluated_at", evaluatedAt); err != nil {
		return RestoreDrillPlan{}, fmt.Errorf("%w: %v", ErrInvalidRestoreDrillPlan, err)
	}
	if !evaluatedAt.After(restore.UpdatedAt) {
		return RestoreDrillPlan{}, fmt.Errorf("%w: evaluated_at must follow restore updated_at", ErrInvalidRestoreDrillPlan)
	}

	required := []ProviderCapability{CapabilityRestore, CapabilityRestoreDrill}
	adapter, err := registry.Resolve(restore.Provider, required...)
	if err != nil {
		return RestoreDrillPlan{}, fmt.Errorf("%w: %w", ErrInvalidRestoreDrillPlan, err)
	}
	backupIDs := append([]string(nil), restore.BackupIDs...)
	sort.Strings(backupIDs)
	provider := cloneProviderMetadata(restore.Provider)
	provider.Capabilities, _ = canonicalCapabilities("provider capabilities", provider.Capabilities, true)

	plan := RestoreDrillPlan{
		SchemaVersion:            RestoreDrillPlanSchemaVersion,
		RestoreID:                restore.ObjectID,
		ScopeID:                  restore.ScopeID,
		RecoveryPointID:          restore.RecoveryPointID,
		BackupIDs:                backupIDs,
		Target:                   restore.Target,
		Provider:                 provider,
		Adapter:                  adapter,
		BasedOnResourceVersion:   restore.ResourceVersion,
		BasedOnGeneration:        restore.Generation,
		EvaluatedAt:              evaluatedAt,
		PlanOnly:                 true,
		ExecutesProvider:         false,
		UsesCredentials:          false,
		ProductionMutation:       false,
		RequiresAuditedChangeJob: true,
		RequiredCapabilities:     append([]ProviderCapability(nil), required...),
		Steps:                    restoreDrillSteps(),
	}
	plan.PlanID, err = restoreDrillPlanID(plan)
	if err != nil {
		return RestoreDrillPlan{}, err
	}
	return plan, nil
}

// RestoreDrillTransitionRequest contains observations supplied by a trusted
// caller after an external audited job. ApplyRestoreDrillTransition validates
// and records them but cannot execute that job or verify an evidence reference
// by dereferencing it.
type RestoreDrillTransitionRequest struct {
	To                   RestoreState                     `json:"to"`
	Precondition         corecontracts.ObjectPrecondition `json:"precondition"`
	OccurredAt           time.Time                        `json:"occurred_at"`
	ProviderOperationID  string                           `json:"provider_operation_id,omitempty"`
	VerificationEvidence []EvidenceReference              `json:"verification_evidence,omitempty"`
	Failure              *FailureMetadata                 `json:"failure,omitempty"`
}

// ApplyRestoreDrillTransition is a pure state/evidence boundary. The opaque
// nextResourceVersion must be allocated by the persistence/synchronization
// layer, not supplied in request JSON. The storage adapter must repeat the
// precondition check atomically when persisting the returned successor.
func ApplyRestoreDrillTransition(registry *AdapterRegistry, current RestoreMetadata, request RestoreDrillTransitionRequest, nextResourceVersion string) (RestoreMetadata, error) {
	if err := ValidateRestoreMetadata(current); err != nil {
		return RestoreMetadata{}, fmt.Errorf("%w: current: %v", ErrInvalidRestoreDrillTransition, err)
	}
	if current.Mode != RestoreIsolatedDrill {
		return RestoreMetadata{}, fmt.Errorf("%w: restore mode must be ISOLATED_DRILL", ErrInvalidRestoreDrillTransition)
	}
	if _, err := registry.Resolve(current.Provider, CapabilityRestore, CapabilityRestoreDrill); err != nil {
		return RestoreMetadata{}, fmt.Errorf("%w: %w", ErrInvalidRestoreDrillTransition, err)
	}
	if err := request.Precondition.ValidateAgainst(current.ObjectMetadata); err != nil {
		return RestoreMetadata{}, fmt.Errorf("%w: %w", ErrInvalidRestoreDrillTransition, err)
	}
	if err := validateTimestamp("occurred_at", request.OccurredAt); err != nil {
		return RestoreMetadata{}, fmt.Errorf("%w: %v", ErrInvalidRestoreDrillTransition, err)
	}
	if !request.OccurredAt.After(current.UpdatedAt) {
		return RestoreMetadata{}, fmt.Errorf("%w: occurred_at must follow current updated_at", ErrInvalidRestoreDrillTransition)
	}
	if !validRestoreDrillEdge(current.State, request.To) {
		return RestoreMetadata{}, fmt.Errorf("%w: transition %s -> %s is not allowed", ErrInvalidRestoreDrillTransition, current.State, request.To)
	}

	next := cloneRestoreMetadata(current)
	next.State = request.To
	next.UpdatedAt = request.OccurredAt
	next.ResourceVersion = nextResourceVersion

	switch request.To {
	case RestoreRunning:
		if request.ProviderOperationID == "" || len(request.VerificationEvidence) != 0 || request.Failure != nil {
			return RestoreMetadata{}, fmt.Errorf("%w: RUNNING requires only a provider operation id", ErrInvalidRestoreDrillTransition)
		}
		next.Provider.OperationID = request.ProviderOperationID
		next.StartedAt = timePointerCopy(request.OccurredAt)
	case RestoreVerifying:
		if request.ProviderOperationID != "" || len(request.VerificationEvidence) != 0 || request.Failure != nil {
			return RestoreMetadata{}, fmt.Errorf("%w: VERIFYING does not accept terminal results", ErrInvalidRestoreDrillTransition)
		}
	case RestoreSucceeded:
		if request.ProviderOperationID != "" || request.Failure != nil {
			return RestoreMetadata{}, fmt.Errorf("%w: SUCCEEDED accepts evidence and no failure", ErrInvalidRestoreDrillTransition)
		}
		evidence, err := canonicalDrillEvidence(request.VerificationEvidence, true, next.StartedAt, request.OccurredAt)
		if err != nil {
			return RestoreMetadata{}, err
		}
		next.CompletedAt = timePointerCopy(request.OccurredAt)
		next.Verification = RestoreVerification{Outcome: VerificationPassed, VerifiedAt: timePointerCopy(request.OccurredAt), Evidence: evidence}
	case RestoreFailed:
		if request.ProviderOperationID != "" || request.Failure == nil {
			return RestoreMetadata{}, fmt.Errorf("%w: FAILED requires failure metadata and no new provider operation", ErrInvalidRestoreDrillTransition)
		}
		next.CompletedAt = timePointerCopy(request.OccurredAt)
		next.Failure = cloneFailure(request.Failure)
		if current.State == RestoreVerifying {
			evidence, err := canonicalDrillEvidence(request.VerificationEvidence, false, next.StartedAt, request.OccurredAt)
			if err != nil {
				return RestoreMetadata{}, err
			}
			next.Verification = RestoreVerification{Outcome: VerificationFailed, VerifiedAt: timePointerCopy(request.OccurredAt), Evidence: evidence}
		} else if len(request.VerificationEvidence) != 0 {
			return RestoreMetadata{}, fmt.Errorf("%w: execution failure cannot contain verification evidence", ErrInvalidRestoreDrillTransition)
		}
	case RestoreCancelled:
		if request.ProviderOperationID != "" || len(request.VerificationEvidence) != 0 || request.Failure != nil {
			return RestoreMetadata{}, fmt.Errorf("%w: CANCELLED cannot contain terminal evidence or failure", ErrInvalidRestoreDrillTransition)
		}
		next.CompletedAt = timePointerCopy(request.OccurredAt)
	}

	if err := ValidateRestoreMetadata(next); err != nil {
		return RestoreMetadata{}, fmt.Errorf("%w: successor: %v", ErrInvalidRestoreDrillTransition, err)
	}
	if err := corecontracts.ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, false); err != nil {
		return RestoreMetadata{}, fmt.Errorf("%w: %w", ErrInvalidRestoreDrillTransition, err)
	}
	return next, nil
}

func validRestoreDrillEdge(from, to RestoreState) bool {
	switch from {
	case RestorePlanned:
		return to == RestoreRunning || to == RestoreCancelled
	case RestoreRunning:
		return to == RestoreVerifying || to == RestoreFailed || to == RestoreCancelled
	case RestoreVerifying:
		return to == RestoreSucceeded || to == RestoreFailed || to == RestoreCancelled
	default:
		return false
	}
}

func canonicalDrillEvidence(evidence []EvidenceReference, passed bool, startedAt *time.Time, observedAt time.Time) ([]EvidenceReference, error) {
	result := append([]EvidenceReference(nil), evidence...)
	if err := validateEvidence("verification_evidence", result, true); err != nil {
		return nil, fmt.Errorf("%w: unverifiable evidence: %v", ErrInvalidRestoreDrillTransition, err)
	}
	if startedAt == nil {
		return nil, fmt.Errorf("%w: unverifiable evidence: restore start is missing", ErrInvalidRestoreDrillTransition)
	}
	for _, item := range result {
		if item.RecordedAt.Before(*startedAt) || item.RecordedAt.After(observedAt) {
			return nil, fmt.Errorf("%w: unverifiable evidence: recorded_at is outside the restore drill interval", ErrInvalidRestoreDrillTransition)
		}
		if item.Kind == EvidenceFencingConfirmation {
			return nil, fmt.Errorf("%w: unverifiable evidence: fencing confirmation is not drill verification evidence", ErrInvalidRestoreDrillTransition)
		}
	}
	if !containsEvidenceKind(result, EvidenceRestoreDrill) {
		return nil, fmt.Errorf("%w: unverifiable evidence: RESTORE_DRILL evidence is required", ErrInvalidRestoreDrillTransition)
	}
	if passed && !containsEvidenceKind(result, EvidenceFunctionalTest) {
		return nil, fmt.Errorf("%w: unverifiable evidence: FUNCTIONAL_TEST evidence is required", ErrInvalidRestoreDrillTransition)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func restoreDrillSteps() []RestoreDrillStep {
	return []RestoreDrillStep{
		{Order: 1, Stage: RestoreDrillPreflight, Action: DrillValidateMetadata, RequiredCapabilities: []ProviderCapability{}, RequiredEvidence: []EvidenceKind{}},
		{Order: 2, Stage: RestoreDrillPreflight, Action: DrillResolveAdapter, RequiredCapabilities: []ProviderCapability{CapabilityRestore, CapabilityRestoreDrill}, RequiredEvidence: []EvidenceKind{}},
		{Order: 3, Stage: RestoreDrillRestore, Action: DrillRestoreIsolatedCopy, RequiredCapabilities: []ProviderCapability{CapabilityRestore, CapabilityRestoreDrill}, RequiredEvidence: []EvidenceKind{}},
		{Order: 4, Stage: RestoreDrillVerification, Action: DrillRecordRestoreProof, RequiredCapabilities: []ProviderCapability{}, RequiredEvidence: []EvidenceKind{EvidenceRestoreDrill}},
		{Order: 5, Stage: RestoreDrillVerification, Action: DrillRunFunctionalTests, RequiredCapabilities: []ProviderCapability{}, RequiredEvidence: []EvidenceKind{EvidenceFunctionalTest}},
		{Order: 6, Stage: RestoreDrillRecord, Action: DrillRecordVerification, RequiredCapabilities: []ProviderCapability{}, RequiredEvidence: []EvidenceKind{EvidenceRestoreDrill, EvidenceFunctionalTest}},
	}
}

func restoreDrillPlanID(plan RestoreDrillPlan) (string, error) {
	fingerprint := struct {
		SchemaVersion          string               `json:"schema_version"`
		RestoreID              string               `json:"restore_id"`
		ScopeID                string               `json:"scope_id"`
		RecoveryPointID        string               `json:"recovery_point_id"`
		BackupIDs              []string             `json:"backup_ids"`
		Target                 ObjectReference      `json:"target"`
		Provider               ProviderMetadata     `json:"provider"`
		Adapter                AdapterDescriptor    `json:"adapter"`
		BasedOnResourceVersion string               `json:"based_on_resource_version"`
		BasedOnGeneration      uint64               `json:"based_on_generation"`
		RequiredCapabilities   []ProviderCapability `json:"required_capabilities"`
		Steps                  []RestoreDrillStep   `json:"steps"`
	}{
		SchemaVersion: plan.SchemaVersion, RestoreID: plan.RestoreID, ScopeID: plan.ScopeID,
		RecoveryPointID: plan.RecoveryPointID, BackupIDs: plan.BackupIDs, Target: plan.Target,
		Provider: plan.Provider, Adapter: plan.Adapter, BasedOnResourceVersion: plan.BasedOnResourceVersion,
		BasedOnGeneration: plan.BasedOnGeneration, RequiredCapabilities: plan.RequiredCapabilities, Steps: plan.Steps,
	}
	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return "", fmt.Errorf("%w: fingerprint: %v", ErrInvalidRestoreDrillPlan, err)
	}
	digest := sha256.Sum256(encoded)
	return "rdp-" + hex.EncodeToString(digest[:]), nil
}

func cloneProviderMetadata(provider ProviderMetadata) ProviderMetadata {
	provider.Capabilities = cloneSlice(provider.Capabilities)
	return provider
}

func cloneRestoreMetadata(restore RestoreMetadata) RestoreMetadata {
	restore.BackupIDs = cloneSlice(restore.BackupIDs)
	restore.RequestedObjects = cloneSlice(restore.RequestedObjects)
	restore.Provider = cloneProviderMetadata(restore.Provider)
	restore.Fencing.TargetIDs = cloneSlice(restore.Fencing.TargetIDs)
	if restore.Fencing.Provider != nil {
		provider := cloneProviderMetadata(*restore.Fencing.Provider)
		restore.Fencing.Provider = &provider
	}
	restore.Fencing.ConfirmedAt = cloneTime(restore.Fencing.ConfirmedAt)
	restore.Fencing.Evidence = cloneSlice(restore.Fencing.Evidence)
	restore.Fencing.Failure = cloneFailure(restore.Fencing.Failure)
	restore.StartedAt = cloneTime(restore.StartedAt)
	restore.CompletedAt = cloneTime(restore.CompletedAt)
	restore.PITRTargetAt = cloneTime(restore.PITRTargetAt)
	restore.Verification.VerifiedAt = cloneTime(restore.Verification.VerifiedAt)
	restore.Verification.Evidence = cloneSlice(restore.Verification.Evidence)
	restore.Failure = cloneFailure(restore.Failure)
	return restore
}

func cloneSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	result := make([]T, len(values))
	copy(result, values)
	return result
}

func timePointerCopy(value time.Time) *time.Time {
	copy := value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return timePointerCopy(*value)
}

func cloneFailure(value *FailureMetadata) *FailureMetadata {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
