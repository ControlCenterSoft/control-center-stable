package recovery

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"control-center/internal/corecontracts"
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid recovery metadata %s: %s", e.Field, e.Message)
}

var (
	identifierPattern      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$`)
	semanticVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	errorCodePattern       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	lsnPattern             = regexp.MustCompile(`^[0-9A-F]+/[0-9A-F]+$`)
)

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

func ValidateRecoveryPoint(point RecoveryPoint) error {
	if point.SchemaVersion != RecoveryPointSchemaVersion {
		return invalid("schema_version", "must be "+RecoveryPointSchemaVersion)
	}
	if err := validateObjectMetadata(point.ObjectMetadata); err != nil {
		return err
	}
	if !validRecoveryPointState(point.State) {
		return invalid("state", "unsupported recovery point state")
	}
	if !validRecoveryPointTrigger(point.Trigger) {
		return invalid("trigger", "unsupported recovery point trigger")
	}
	if !validConsistency(point.Consistency) {
		return invalid("consistency", "unsupported consistency level")
	}
	if err := validateObjectReferences("objects", point.Objects, true); err != nil {
		return err
	}
	if err := validateObjectReferenceScopes("objects", point.Objects, point.ScopeID); err != nil {
		return err
	}
	if point.BackupIDs == nil {
		return invalid("backup_ids", "must be an array")
	}
	if err := validateIDSet("backup_ids", point.BackupIDs, false); err != nil {
		return err
	}
	if point.Trigger == TriggerPreChange || point.Trigger == TriggerPreDestructive {
		if !identifierPattern.MatchString(point.ChangeID) {
			return invalid("change_id", "pre-change recovery point requires a change id")
		}
	} else if point.ChangeID != "" && !identifierPattern.MatchString(point.ChangeID) {
		return invalid("change_id", "must be a canonical identifier")
	}
	if err := validateTimestamp("created_at", point.CreatedAt); err != nil {
		return err
	}
	if err := validateTimestamp("updated_at", point.UpdatedAt); err != nil {
		return err
	}
	if point.UpdatedAt.Before(point.CreatedAt) {
		return invalid("updated_at", "must not precede created_at")
	}
	if point.ExpiresAt != nil {
		if err := validateTimestamp("expires_at", *point.ExpiresAt); err != nil {
			return err
		}
		if !point.ExpiresAt.After(point.CreatedAt) {
			return invalid("expires_at", "must be after created_at")
		}
	}
	if point.Failure != nil {
		if err := validateFailure("failure", *point.Failure); err != nil {
			return err
		}
	}
	switch point.State {
	case RecoveryPointCreating:
		if point.Failure != nil {
			return invalid("failure", "creating recovery point cannot have failure metadata")
		}
	case RecoveryPointReady:
		if len(point.BackupIDs) == 0 {
			return invalid("backup_ids", "ready recovery point requires at least one backup")
		}
		if point.Failure != nil {
			return invalid("failure", "ready recovery point cannot have failure metadata")
		}
	case RecoveryPointPartial:
		if len(point.BackupIDs) == 0 || point.Failure == nil {
			return invalid("state", "partial recovery point requires a backup and failure metadata")
		}
	case RecoveryPointFailed:
		if point.Failure == nil {
			return invalid("failure", "failed recovery point requires failure metadata")
		}
		if len(point.BackupIDs) != 0 {
			return invalid("state", "failed recovery point cannot contain successful backup references; use PARTIAL")
		}
	case RecoveryPointExpired:
		if point.ExpiresAt == nil || point.ExpiresAt.After(point.UpdatedAt) {
			return invalid("expires_at", "expired recovery point requires an elapsed expiration timestamp")
		}
	}
	if point.State != RecoveryPointExpired && point.ExpiresAt != nil && !point.ExpiresAt.After(point.UpdatedAt) {
		return invalid("state", "elapsed recovery point must be marked EXPIRED")
	}
	return nil
}

// ValidateProviderMetadata validates provider identity without assuming a
// specific operation. Backup/restore validators add repository and capability
// requirements for their own context.
func ValidateProviderMetadata(provider ProviderMetadata) error {
	return validateProvider("provider", provider, false)
}

// ValidateFencingMetadata validates reusable fencing evidence for restore or
// future failover contracts. It never calls the fencing provider.
func ValidateFencingMetadata(fencing FencingMetadata) error {
	return validateFencing(fencing)
}

func ValidateBackupMetadata(backup BackupMetadata) error {
	if backup.SchemaVersion != BackupMetadataSchemaVersion {
		return invalid("schema_version", "must be "+BackupMetadataSchemaVersion)
	}
	if err := validateObjectMetadata(backup.ObjectMetadata); err != nil {
		return err
	}
	if !identifierPattern.MatchString(backup.RecoveryPointID) {
		return invalid("recovery_point_id", "must be a canonical identifier")
	}
	if err := validateObjectReference("target", backup.Target); err != nil {
		return err
	}
	if backup.Target.ScopeID != backup.ScopeID {
		return invalid("target.scope_id", "must match backup scope_id")
	}
	if !validBackupMode(backup.Mode) {
		return invalid("mode", "unsupported backup mode")
	}
	if !validBackupState(backup.State) {
		return invalid("state", "unsupported backup state")
	}
	if err := validateProvider("provider", backup.Provider, true); err != nil {
		return err
	}
	if !providerHas(backup.Provider, CapabilityBackup) {
		return invalid("provider.capabilities", "backup provider must declare BACKUP")
	}
	if err := validateBackupModeMetadata(backup); err != nil {
		return err
	}
	if backup.Artifact != nil {
		if err := validateArtifact(*backup.Artifact, backup.Provider.RepositoryID); err != nil {
			return err
		}
	}
	if backup.PITR != nil {
		if err := validatePITR(*backup.PITR); err != nil {
			return err
		}
	}
	if err := validateTimestamp("requested_at", backup.RequestedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("started_at", backup.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("data_captured_at", backup.DataCapturedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("completed_at", backup.CompletedAt); err != nil {
		return err
	}
	if backup.RequestedAt.After(backup.UpdatedAt) {
		return invalid("requested_at", "must not follow object metadata updated_at")
	}
	if backup.StartedAt != nil && backup.StartedAt.Before(backup.RequestedAt) {
		return invalid("started_at", "must not precede requested_at")
	}
	if backup.StartedAt != nil && backup.DataCapturedAt != nil && backup.DataCapturedAt.Before(*backup.StartedAt) {
		return invalid("data_captured_at", "must not precede started_at")
	}
	if backup.DataCapturedAt != nil && backup.StartedAt == nil {
		return invalid("data_captured_at", "requires started_at")
	}
	if backup.StartedAt != nil && backup.CompletedAt != nil && backup.CompletedAt.Before(*backup.StartedAt) {
		return invalid("completed_at", "must not precede started_at")
	}
	if backup.DataCapturedAt != nil && backup.CompletedAt != nil && backup.CompletedAt.Before(*backup.DataCapturedAt) {
		return invalid("completed_at", "must not precede data_captured_at")
	}
	if backup.CompletedAt != nil && backup.StartedAt == nil && backup.CompletedAt.Before(backup.RequestedAt) {
		return invalid("completed_at", "must not precede requested_at")
	}
	for _, observed := range []struct {
		field string
		value *time.Time
	}{
		{field: "started_at", value: backup.StartedAt},
		{field: "data_captured_at", value: backup.DataCapturedAt},
		{field: "completed_at", value: backup.CompletedAt},
		{field: "health.last_verified_at", value: backup.Health.LastVerifiedAt},
	} {
		if observed.value != nil && observed.value.After(backup.UpdatedAt) {
			return invalid(observed.field, "must not follow object metadata updated_at")
		}
	}
	if err := validateBackupHealth(backup.Health, backup.State); err != nil {
		return err
	}
	if backup.CompletedAt != nil && backup.Health.LastVerifiedAt != nil && backup.Health.LastVerifiedAt.Before(*backup.CompletedAt) {
		return invalid("health.last_verified_at", "must not precede completed_at")
	}
	if backup.CompletedAt != nil {
		for _, evidence := range backup.Health.Evidence {
			if evidence.RecordedAt.Before(*backup.CompletedAt) {
				return invalid("health.evidence.recorded_at", "must not precede completed_at")
			}
		}
	}
	for _, evidence := range backup.Health.Evidence {
		if evidence.RecordedAt.After(backup.UpdatedAt) {
			return invalid("health.evidence.recorded_at", "must not follow object metadata updated_at")
		}
	}
	if backup.Failure != nil {
		if err := validateFailure("failure", *backup.Failure); err != nil {
			return err
		}
	}
	switch backup.State {
	case BackupPlanned:
		if backup.StartedAt != nil || backup.DataCapturedAt != nil || backup.CompletedAt != nil || backup.Artifact != nil || backup.Failure != nil || backup.Provider.OperationID != "" {
			return invalid("state", "planned backup cannot contain execution results")
		}
	case BackupRunning:
		if backup.StartedAt == nil || backup.CompletedAt != nil || backup.Artifact != nil || backup.Failure != nil || backup.Provider.OperationID == "" {
			return invalid("state", "running backup requires start/provider operation and no terminal result")
		}
	case BackupCompleted:
		if backup.StartedAt == nil || backup.DataCapturedAt == nil || backup.CompletedAt == nil || backup.Artifact == nil || backup.Failure != nil || backup.Provider.OperationID == "" {
			return invalid("state", "completed backup requires timestamps, artifact, and provider operation")
		}
	case BackupFailed:
		if backup.CompletedAt == nil || backup.Artifact != nil || backup.Failure == nil || backup.Provider.OperationID == "" || backup.Health.State != BackupHealthFailed {
			return invalid("state", "failed backup requires terminal failure metadata and no artifact")
		}
	case BackupExpired:
		if backup.StartedAt == nil || backup.DataCapturedAt == nil || backup.CompletedAt == nil || backup.Artifact == nil || backup.Failure != nil || backup.Provider.OperationID == "" {
			return invalid("state", "expired backup retains its completed artifact metadata")
		}
	case BackupDeleted:
		if backup.StartedAt == nil || backup.DataCapturedAt == nil || backup.CompletedAt == nil || backup.Artifact != nil || backup.Failure != nil || backup.Provider.OperationID == "" {
			return invalid("state", "deleted backup must retain completion metadata but not an artifact reference")
		}
	}
	return nil
}

func ValidateRestoreMetadata(restore RestoreMetadata) error {
	if restore.SchemaVersion != RestoreMetadataSchemaVersion {
		return invalid("schema_version", "must be "+RestoreMetadataSchemaVersion)
	}
	if err := validateObjectMetadata(restore.ObjectMetadata); err != nil {
		return err
	}
	if !identifierPattern.MatchString(restore.RecoveryPointID) {
		return invalid("recovery_point_id", "must be a canonical identifier")
	}
	if err := validateIDSet("backup_ids", restore.BackupIDs, true); err != nil {
		return err
	}
	if !validRestoreMode(restore.Mode) {
		return invalid("mode", "unsupported restore mode")
	}
	if !validRestoreState(restore.State) {
		return invalid("state", "unsupported restore state")
	}
	if err := validateObjectReference("target", restore.Target); err != nil {
		return err
	}
	if restore.Target.ScopeID != restore.ScopeID {
		return invalid("target.scope_id", "must match restore scope_id")
	}
	if restore.RequestedObjects == nil {
		return invalid("requested_objects", "must be an array")
	}
	if err := validateObjectReferenceScopes("requested_objects", restore.RequestedObjects, restore.ScopeID); err != nil {
		return err
	}
	if err := validateProvider("provider", restore.Provider, true); err != nil {
		return err
	}
	if !providerHas(restore.Provider, CapabilityRestore) {
		return invalid("provider.capabilities", "restore provider must declare RESTORE")
	}
	if err := validateRestoreModeMetadata(restore); err != nil {
		return err
	}
	if err := validateFencing(restore.Fencing); err != nil {
		return err
	}
	if err := validateTimestamp("requested_at", restore.RequestedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("started_at", restore.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("completed_at", restore.CompletedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("pitr_target_at", restore.PITRTargetAt); err != nil {
		return err
	}
	if restore.StartedAt != nil && restore.StartedAt.Before(restore.RequestedAt) {
		return invalid("started_at", "must not precede requested_at")
	}
	if restore.StartedAt != nil && restore.Provider.OperationID == "" {
		return invalid("provider.operation_id", "started restore requires a provider operation")
	}
	if restore.PITRTargetAt != nil && restore.PITRTargetAt.After(restore.RequestedAt) {
		return invalid("pitr_target_at", "must not be in the future relative to requested_at")
	}
	if restore.Fencing.ConfirmedAt != nil && restore.StartedAt != nil && restore.Fencing.ConfirmedAt.After(*restore.StartedAt) {
		return invalid("fencing.confirmed_at", "must not follow restore started_at")
	}
	if restore.Fencing.ConfirmedAt != nil && restore.Fencing.ConfirmedAt.Before(restore.RequestedAt) {
		return invalid("fencing.confirmed_at", "must not precede requested_at")
	}
	for _, evidence := range restore.Fencing.Evidence {
		if evidence.RecordedAt.Before(restore.RequestedAt) {
			return invalid("fencing.evidence.recorded_at", "must not precede requested_at")
		}
	}
	if restore.CompletedAt != nil {
		lowerBound := restore.RequestedAt
		if restore.StartedAt != nil {
			lowerBound = *restore.StartedAt
		}
		if restore.CompletedAt.Before(lowerBound) {
			return invalid("completed_at", "must not precede request/start")
		}
	}
	if restore.RequestedAt.After(restore.UpdatedAt) {
		return invalid("requested_at", "must not follow object metadata updated_at")
	}
	for _, observed := range []struct {
		field string
		value *time.Time
	}{
		{field: "started_at", value: restore.StartedAt},
		{field: "completed_at", value: restore.CompletedAt},
		{field: "fencing.confirmed_at", value: restore.Fencing.ConfirmedAt},
		{field: "verification.verified_at", value: restore.Verification.VerifiedAt},
	} {
		if observed.value != nil && observed.value.After(restore.UpdatedAt) {
			return invalid(observed.field, "must not follow object metadata updated_at")
		}
	}
	for _, observed := range []struct {
		field    string
		evidence []EvidenceReference
	}{
		{field: "fencing.evidence.recorded_at", evidence: restore.Fencing.Evidence},
		{field: "verification.evidence.recorded_at", evidence: restore.Verification.Evidence},
	} {
		for _, evidence := range observed.evidence {
			if evidence.RecordedAt.After(restore.UpdatedAt) {
				return invalid(observed.field, "must not follow object metadata updated_at")
			}
		}
	}
	if err := validateRestoreVerification(restore.Verification); err != nil {
		return err
	}
	if restore.Verification.VerifiedAt != nil {
		lowerBound := restore.RequestedAt
		if restore.StartedAt != nil {
			lowerBound = *restore.StartedAt
		}
		if restore.Verification.VerifiedAt.Before(lowerBound) {
			return invalid("verification.verified_at", "must not precede request/start")
		}
	}
	if restore.Verification.Outcome != VerificationPending && restore.StartedAt == nil {
		return invalid("verification.outcome", "terminal verification requires started_at")
	}
	if restore.Failure != nil {
		if err := validateFailure("failure", *restore.Failure); err != nil {
			return err
		}
	}
	if err := validateRestoreFencingContext(restore); err != nil {
		return err
	}
	switch restore.State {
	case RestorePlanned:
		if restore.StartedAt != nil || restore.CompletedAt != nil || restore.Provider.OperationID != "" || restore.Verification.Outcome != VerificationPending || restore.Failure != nil {
			return invalid("state", "planned restore cannot contain execution results")
		}
	case RestoreRunning, RestoreVerifying:
		if restore.StartedAt == nil || restore.CompletedAt != nil || restore.Provider.OperationID == "" || restore.Verification.Outcome != VerificationPending || restore.Failure != nil {
			return invalid("state", "active restore requires start/provider operation and no terminal result")
		}
	case RestoreSucceeded:
		if restore.StartedAt == nil || restore.CompletedAt == nil || restore.Provider.OperationID == "" || restore.Verification.Outcome != VerificationPassed || restore.Failure != nil {
			return invalid("state", "successful restore requires completed execution and passed verification")
		}
	case RestoreFailed:
		providerOperationMissing := restore.Provider.OperationID == "" && restore.Fencing.State != FencingFailed
		if restore.CompletedAt == nil || providerOperationMissing || restore.Failure == nil || restore.Verification.Outcome == VerificationPassed {
			return invalid("state", "failed restore requires terminal failure and cannot have passed verification")
		}
	case RestoreCancelled:
		operationStarted := restore.StartedAt != nil
		operationAssigned := restore.Provider.OperationID != ""
		if restore.CompletedAt == nil || restore.Verification.Outcome != VerificationPending || restore.Failure != nil || operationStarted != operationAssigned {
			return invalid("state", "cancelled restore requires completion, paired start/provider operation metadata, and no verification or failure")
		}
	}
	if restore.Verification.VerifiedAt != nil && restore.CompletedAt != nil && restore.Verification.VerifiedAt.After(*restore.CompletedAt) {
		return invalid("verification.verified_at", "must not follow completed_at")
	}
	if restore.State == RestoreSucceeded && restore.Mode == RestoreIsolatedDrill {
		if !containsEvidenceKind(restore.Verification.Evidence, EvidenceRestoreDrill) || !containsEvidenceKind(restore.Verification.Evidence, EvidenceFunctionalTest) {
			return invalid("verification.evidence", "successful restore drill requires restore-drill and functional-test evidence")
		}
	}
	return nil
}

func ValidateRecoveryObjectiveEvidence(objective RecoveryObjectiveEvidence) error {
	if objective.SchemaVersion != ObjectiveEvidenceSchemaVersion {
		return invalid("schema_version", "must be "+ObjectiveEvidenceSchemaVersion)
	}
	if err := validateObjectMetadata(objective.ObjectMetadata); err != nil {
		return err
	}
	for _, item := range []struct {
		field string
		value string
	}{
		{field: "policy_id", value: objective.PolicyID},
		{field: "recovery_point_id", value: objective.RecoveryPointID},
		{field: "restore_id", value: objective.RestoreID},
	} {
		if !identifierPattern.MatchString(item.value) {
			return invalid(item.field, "must be a canonical identifier")
		}
	}
	if err := validateObjectReference("target", objective.Target); err != nil {
		return err
	}
	if objective.Target.ScopeID != objective.ScopeID {
		return invalid("target.scope_id", "must match objective scope_id")
	}
	if objective.TargetRPOSeconds == 0 || objective.TargetRTOSeconds == 0 {
		return invalid("target_rpo_seconds", "RPO and RTO targets must be positive")
	}
	if err := validateTimestamp("measured_at", objective.MeasuredAt); err != nil {
		return err
	}
	if objective.MeasuredAt.After(objective.UpdatedAt) {
		return invalid("measured_at", "must not follow object metadata updated_at")
	}
	if objective.Result != ObjectivePassed && objective.Result != ObjectiveFailed {
		return invalid("result", "unsupported objective result")
	}
	if err := validateEvidence("evidence", objective.Evidence, true); err != nil {
		return err
	}
	if !containsEvidenceKind(objective.Evidence, EvidenceRestoreDrill) {
		return invalid("evidence", "RPO/RTO measurement requires restore-drill evidence")
	}
	for _, evidence := range objective.Evidence {
		if evidence.RecordedAt.After(objective.MeasuredAt) {
			return invalid("evidence.recorded_at", "must not follow measured_at")
		}
	}
	switch objective.Result {
	case ObjectivePassed:
		if objective.ObservedRPOSeconds == nil || objective.ObservedRTOSeconds == nil {
			return invalid("result", "passed objective requires observed RPO and RTO")
		}
		if *objective.ObservedRPOSeconds > objective.TargetRPOSeconds || *objective.ObservedRTOSeconds > objective.TargetRTOSeconds {
			return invalid("result", "passed objective exceeds the configured target")
		}
		if strings.TrimSpace(objective.FailureReason) != "" {
			return invalid("failure_reason", "passed objective cannot contain a failure reason")
		}
		if !containsEvidenceKind(objective.Evidence, EvidenceFunctionalTest) {
			return invalid("evidence", "passed objective requires functional-test evidence")
		}
	case ObjectiveFailed:
		exceedsTarget := objective.ObservedRPOSeconds != nil && *objective.ObservedRPOSeconds > objective.TargetRPOSeconds || objective.ObservedRTOSeconds != nil && *objective.ObservedRTOSeconds > objective.TargetRTOSeconds
		if !exceedsTarget && strings.TrimSpace(objective.FailureReason) == "" {
			return invalid("failure_reason", "failed objective requires an exceeded target or failure reason")
		}
		if strings.TrimSpace(objective.FailureReason) != objective.FailureReason || len(objective.FailureReason) > 512 {
			return invalid("failure_reason", "must be canonical and not exceed 512 bytes")
		}
	}
	return nil
}

func validateObjectMetadata(metadata corecontracts.ObjectMetadata) error {
	if err := metadata.Validate(); err != nil {
		return invalid("object_metadata", err.Error())
	}
	if err := validateTimestamp("created_at", metadata.CreatedAt); err != nil {
		return err
	}
	return validateTimestamp("updated_at", metadata.UpdatedAt)
}

func validateObjectReferences(field string, references []ObjectReference, required bool) error {
	if required && len(references) == 0 {
		return invalid(field, "must not be empty")
	}
	seen := make(map[string]struct{}, len(references))
	for index, reference := range references {
		prefix := fmt.Sprintf("%s[%d]", field, index)
		if err := validateObjectReference(prefix, reference); err != nil {
			return err
		}
		key := string(reference.Kind) + "\x00" + reference.ScopeID + "\x00" + reference.ObjectID
		if _, exists := seen[key]; exists {
			return invalid(field, "contains a duplicate object reference")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateObjectReferenceScopes(field string, references []ObjectReference, scopeID string) error {
	for index, reference := range references {
		if reference.ScopeID != scopeID {
			return invalid(fmt.Sprintf("%s[%d].scope_id", field, index), "must match enclosing scope_id")
		}
	}
	return nil
}

func validateObjectReference(field string, reference ObjectReference) error {
	if !validObjectKind(reference.Kind) {
		return invalid(field+".kind", "unsupported object kind")
	}
	if !identifierPattern.MatchString(reference.ObjectID) {
		return invalid(field+".object_id", "must be a canonical identifier")
	}
	if !identifierPattern.MatchString(reference.ScopeID) {
		return invalid(field+".scope_id", "must be a canonical identifier")
	}
	if reference.Statefulness != Stateless && reference.Statefulness != Stateful {
		return invalid(field+".statefulness", "must be STATELESS or STATEFUL")
	}
	return nil
}

func validateIDSet(field string, values []string, required bool) error {
	if required && len(values) == 0 {
		return invalid(field, "must not be empty")
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !identifierPattern.MatchString(value) {
			return invalid(fmt.Sprintf("%s[%d]", field, index), "must be a canonical identifier")
		}
		if _, exists := seen[value]; exists {
			return invalid(field, "contains duplicate "+value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateProvider(field string, provider ProviderMetadata, repositoryRequired bool) error {
	if !identifierPattern.MatchString(provider.ProviderID) {
		return invalid(field+".provider_id", "must be a canonical identifier")
	}
	if !identifierPattern.MatchString(provider.AdapterID) {
		return invalid(field+".adapter_id", "must be a canonical identifier")
	}
	if !semanticVersionPattern.MatchString(provider.Version) {
		return invalid(field+".version", "must be a semantic version")
	}
	if repositoryRequired && !identifierPattern.MatchString(provider.RepositoryID) {
		return invalid(field+".repository_id", "backup/restore provider requires a repository id")
	}
	if !repositoryRequired && provider.RepositoryID != "" && !identifierPattern.MatchString(provider.RepositoryID) {
		return invalid(field+".repository_id", "must be a canonical identifier")
	}
	if provider.OperationID != "" && !identifierPattern.MatchString(provider.OperationID) {
		return invalid(field+".operation_id", "must be a canonical identifier")
	}
	if len(provider.Capabilities) == 0 {
		return invalid(field+".capabilities", "must not be empty")
	}
	seen := map[ProviderCapability]struct{}{}
	for index, capability := range provider.Capabilities {
		if !validProviderCapability(capability) {
			return invalid(fmt.Sprintf("%s.capabilities[%d]", field, index), "unsupported provider capability")
		}
		if _, exists := seen[capability]; exists {
			return invalid(field+".capabilities", "contains duplicate "+string(capability))
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validateArtifact(artifact BackupArtifact, providerRepositoryID string) error {
	if !identifierPattern.MatchString(artifact.RepositoryID) || artifact.RepositoryID != providerRepositoryID {
		return invalid("artifact.repository_id", "must match the provider repository")
	}
	if !validObjectKey(artifact.ObjectKey) {
		return invalid("artifact.object_key", "must be a safe repository-relative key")
	}
	if artifact.SizeBytes == 0 {
		return invalid("artifact.size_bytes", "must be positive")
	}
	if !digestPattern.MatchString(artifact.Digest) {
		return invalid("artifact.digest", "must be a sha256 digest")
	}
	switch artifact.Encryption {
	case EncryptionNone, EncryptionProviderManaged:
		if artifact.EncryptionKeyID != "" {
			return invalid("artifact.encryption_key_id", "is only allowed for customer-managed encryption")
		}
	case EncryptionCustomerManaged:
		if !identifierPattern.MatchString(artifact.EncryptionKeyID) {
			return invalid("artifact.encryption_key_id", "customer-managed encryption requires a key reference")
		}
	default:
		return invalid("artifact.encryption", "unsupported encryption mode")
	}
	return nil
}

func validObjectKey(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validatePITR(window PITRWindow) error {
	if window.Timeline == 0 {
		return invalid("pitr.timeline", "must be positive")
	}
	if !lsnPattern.MatchString(window.StartLSN) || !lsnPattern.MatchString(window.EndLSN) || comparePostgresLSN(window.StartLSN, window.EndLSN) >= 0 {
		return invalid("pitr", "requires ordered uppercase PostgreSQL LSN bounds")
	}
	if err := validateTimestamp("pitr.earliest_restore_at", window.EarliestRestoreAt); err != nil {
		return err
	}
	if err := validateTimestamp("pitr.latest_restore_at", window.LatestRestoreAt); err != nil {
		return err
	}
	if window.LatestRestoreAt.Before(window.EarliestRestoreAt) {
		return invalid("pitr.latest_restore_at", "must not precede earliest_restore_at")
	}
	return nil
}

func comparePostgresLSN(left, right string) int {
	leftParts := strings.Split(left, "/")
	rightParts := strings.Split(right, "/")
	for index := 0; index < 2; index++ {
		leftPart := strings.TrimLeft(leftParts[index], "0")
		rightPart := strings.TrimLeft(rightParts[index], "0")
		if leftPart == "" {
			leftPart = "0"
		}
		if rightPart == "" {
			rightPart = "0"
		}
		if len(leftPart) < len(rightPart) {
			return -1
		}
		if len(leftPart) > len(rightPart) {
			return 1
		}
		if comparison := strings.Compare(leftPart, rightPart); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func validateBackupModeMetadata(backup BackupMetadata) error {
	switch backup.Mode {
	case BackupIncremental, BackupDifferential:
		if !identifierPattern.MatchString(backup.ParentBackupID) || backup.ParentBackupID == backup.ObjectID {
			return invalid("parent_backup_id", "incremental/differential backup requires a different parent")
		}
	default:
		if backup.ParentBackupID != "" {
			return invalid("parent_backup_id", "is only valid for incremental/differential backup")
		}
	}
	if backup.Mode == BackupBaseWAL {
		if backup.PITR == nil {
			return invalid("pitr", "BASE_WAL backup requires a PITR window")
		}
		for _, capability := range []ProviderCapability{CapabilityBaseBackup, CapabilityWALArchive, CapabilityPITR} {
			if !providerHas(backup.Provider, capability) {
				return invalid("provider.capabilities", "BASE_WAL provider is missing "+string(capability))
			}
		}
	} else if backup.PITR != nil {
		return invalid("pitr", "is only valid for BASE_WAL backup")
	}
	if backup.Mode == BackupSnapshot && !providerHas(backup.Provider, CapabilitySnapshot) {
		return invalid("provider.capabilities", "SNAPSHOT mode requires SNAPSHOT capability")
	}
	if backup.Mode == BackupObjectVersion && !providerHas(backup.Provider, CapabilityObjectVersion) {
		return invalid("provider.capabilities", "OBJECT_VERSION mode requires OBJECT_VERSION capability")
	}
	return nil
}

func validateBackupHealth(health BackupHealth, backupState BackupState) error {
	if !validBackupHealthState(health.State) {
		return invalid("health.state", "unsupported backup health state")
	}
	if err := validateOptionalTimestamp("health.last_verified_at", health.LastVerifiedAt); err != nil {
		return err
	}
	if health.Evidence == nil {
		return invalid("health.evidence", "must be an array")
	}
	if err := validateEvidence("health.evidence", health.Evidence, false); err != nil {
		return err
	}
	if health.RestoreID != "" && !identifierPattern.MatchString(health.RestoreID) {
		return invalid("health.restore_id", "must be a canonical identifier")
	}
	switch health.State {
	case BackupHealthUnknown:
		if backupState != BackupPlanned && backupState != BackupRunning {
			return invalid("health.state", "UNKNOWN is only valid before backup completion")
		}
		if health.LastVerifiedAt != nil || health.RestoreID != "" || len(health.Evidence) != 0 {
			return invalid("health", "unknown health cannot have verification evidence")
		}
	case BackupHealthUnverified:
		if backupState == BackupPlanned || backupState == BackupRunning || backupState == BackupFailed {
			return invalid("health.state", "UNVERIFIED requires completed backup metadata")
		}
		if health.LastVerifiedAt != nil || health.RestoreID != "" || len(health.Evidence) != 0 {
			return invalid("health", "unverified backup cannot have restore evidence")
		}
	case BackupHealthVerified:
		if backupState != BackupCompleted && backupState != BackupExpired && backupState != BackupDeleted {
			return invalid("health.state", "VERIFIED requires terminal successful backup metadata")
		}
		if health.LastVerifiedAt == nil || !identifierPattern.MatchString(health.RestoreID) || !containsEvidenceKind(health.Evidence, EvidenceRestoreDrill) || !containsEvidenceKind(health.Evidence, EvidenceFunctionalTest) {
			return invalid("health", "VERIFIED requires timestamp, restore id, restore-drill evidence, and functional-test evidence")
		}
	case BackupHealthDegraded:
		if backupState != BackupCompleted && backupState != BackupExpired && backupState != BackupDeleted {
			return invalid("health.state", "DEGRADED requires terminal successful backup metadata")
		}
		if health.LastVerifiedAt == nil || !identifierPattern.MatchString(health.RestoreID) || !containsEvidenceKind(health.Evidence, EvidenceRestoreDrill) {
			return invalid("health", "DEGRADED requires timestamp, restore id, and restore-drill evidence")
		}
	case BackupHealthFailed:
		if backupState != BackupFailed || health.LastVerifiedAt != nil || health.RestoreID != "" || len(health.Evidence) != 0 {
			return invalid("health", "FAILED represents backup execution failure, not restore verification")
		}
	}
	if health.LastVerifiedAt != nil {
		for _, evidence := range health.Evidence {
			if evidence.RecordedAt.After(*health.LastVerifiedAt) {
				return invalid("health.evidence.recorded_at", "must not follow last_verified_at")
			}
		}
	}
	return nil
}

func validateRestoreModeMetadata(restore RestoreMetadata) error {
	if restore.Mode == RestoreObjectLevel {
		if err := validateObjectReferences("requested_objects", restore.RequestedObjects, true); err != nil {
			return err
		}
		if !providerHas(restore.Provider, CapabilityObjectRestore) {
			return invalid("provider.capabilities", "OBJECT_LEVEL requires OBJECT_RESTORE capability")
		}
	} else if len(restore.RequestedObjects) != 0 {
		return invalid("requested_objects", "is only valid for OBJECT_LEVEL restore")
	}
	if restore.Mode == RestorePITR {
		if restore.PITRTargetAt == nil {
			return invalid("pitr_target_at", "PITR restore requires a target timestamp")
		}
		if !providerHas(restore.Provider, CapabilityPITR) {
			return invalid("provider.capabilities", "PITR restore requires PITR capability")
		}
	} else if restore.PITRTargetAt != nil {
		return invalid("pitr_target_at", "is only valid for PITR restore")
	}
	if restore.Mode == RestoreIsolatedDrill && !providerHas(restore.Provider, CapabilityRestoreDrill) {
		return invalid("provider.capabilities", "ISOLATED_DRILL requires RESTORE_DRILL capability")
	}
	return nil
}

func validateFencing(fencing FencingMetadata) error {
	if fencing.State != FencingNotRequired && fencing.State != FencingRequired && fencing.State != FencingConfirmed && fencing.State != FencingFailed {
		return invalid("fencing.state", "unsupported fencing state")
	}
	if fencing.TargetIDs == nil {
		return invalid("fencing.target_ids", "must be an array")
	}
	if err := validateIDSet("fencing.target_ids", fencing.TargetIDs, fencing.State != FencingNotRequired); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("fencing.confirmed_at", fencing.ConfirmedAt); err != nil {
		return err
	}
	if fencing.Evidence == nil {
		return invalid("fencing.evidence", "must be an array")
	}
	if err := validateEvidence("fencing.evidence", fencing.Evidence, false); err != nil {
		return err
	}
	if fencing.Failure != nil {
		if err := validateFailure("fencing.failure", *fencing.Failure); err != nil {
			return err
		}
	}
	if fencing.Provider != nil {
		if err := validateProvider("fencing.provider", *fencing.Provider, false); err != nil {
			return err
		}
		if !providerHas(*fencing.Provider, CapabilityFencing) {
			return invalid("fencing.provider.capabilities", "fencing provider must declare FENCING")
		}
	}
	switch fencing.State {
	case FencingNotRequired:
		if len(fencing.TargetIDs) != 0 || fencing.Epoch != 0 || fencing.Provider != nil || fencing.ConfirmedAt != nil || len(fencing.Evidence) != 0 || fencing.Failure != nil {
			return invalid("fencing", "NOT_REQUIRED must not contain fencing operation metadata")
		}
	case FencingRequired:
		if fencing.Epoch == 0 || fencing.Provider == nil || fencing.Provider.OperationID != "" || fencing.ConfirmedAt != nil || len(fencing.Evidence) != 0 || fencing.Failure != nil {
			return invalid("fencing", "REQUIRED describes a planned fence without execution results")
		}
	case FencingConfirmed:
		if fencing.Epoch == 0 || fencing.Provider == nil || fencing.Provider.OperationID == "" || fencing.ConfirmedAt == nil || !containsEvidenceKind(fencing.Evidence, EvidenceFencingConfirmation) || fencing.Failure != nil {
			return invalid("fencing", "CONFIRMED requires provider operation, timestamp, and confirmation evidence")
		}
		for _, evidence := range fencing.Evidence {
			if evidence.RecordedAt.After(*fencing.ConfirmedAt) {
				return invalid("fencing.evidence.recorded_at", "must not follow confirmed_at")
			}
		}
	case FencingFailed:
		if fencing.Epoch == 0 || fencing.Provider == nil || fencing.Provider.OperationID == "" || fencing.ConfirmedAt != nil || fencing.Failure == nil {
			return invalid("fencing", "FAILED requires provider operation and failure metadata")
		}
	}
	return nil
}

func validateRestoreFencingContext(restore RestoreMetadata) error {
	requiresFencing := restore.Target.Statefulness == Stateful && (restore.Mode == RestoreInPlace || restore.Mode == RestorePITR)
	if !requiresFencing {
		if restore.Fencing.State != FencingNotRequired {
			return invalid("fencing.state", "fencing is only allowed for in-place/PITR stateful restore")
		}
		return nil
	}
	if restore.State == RestorePlanned || restore.State == RestoreCancelled && restore.StartedAt == nil {
		if restore.Fencing.State != FencingRequired && restore.Fencing.State != FencingConfirmed {
			return invalid("fencing.state", "unstarted stateful restore requires a fencing plan")
		}
		return nil
	}
	if restore.State == RestoreFailed && restore.Fencing.State == FencingFailed {
		if restore.StartedAt != nil || restore.Provider.OperationID != "" || restore.Verification.Outcome != VerificationPending {
			return invalid("state", "restore blocked by failed fencing cannot contain restore execution results")
		}
		return nil
	}
	if restore.Fencing.State != FencingConfirmed {
		return invalid("fencing.state", "stateful restore cannot execute before fencing confirmation")
	}
	return nil
}

func validateRestoreVerification(verification RestoreVerification) error {
	if verification.Outcome != VerificationPending && verification.Outcome != VerificationPassed && verification.Outcome != VerificationFailed {
		return invalid("verification.outcome", "unsupported verification outcome")
	}
	if err := validateOptionalTimestamp("verification.verified_at", verification.VerifiedAt); err != nil {
		return err
	}
	if verification.Evidence == nil {
		return invalid("verification.evidence", "must be an array")
	}
	if err := validateEvidence("verification.evidence", verification.Evidence, false); err != nil {
		return err
	}
	if verification.Outcome == VerificationPending {
		if verification.VerifiedAt != nil || len(verification.Evidence) != 0 {
			return invalid("verification", "PENDING cannot contain verification results")
		}
		return nil
	}
	if verification.VerifiedAt == nil || len(verification.Evidence) == 0 {
		return invalid("verification", "terminal verification requires timestamp and evidence")
	}
	for _, evidence := range verification.Evidence {
		if evidence.RecordedAt.After(*verification.VerifiedAt) {
			return invalid("verification.evidence.recorded_at", "must not follow verified_at")
		}
	}
	return nil
}

func validateEvidence(field string, evidence []EvidenceReference, required bool) error {
	if required && len(evidence) == 0 {
		return invalid(field, "must not be empty")
	}
	seen := make(map[string]struct{}, len(evidence))
	for index, item := range evidence {
		prefix := fmt.Sprintf("%s[%d]", field, index)
		if !identifierPattern.MatchString(item.ID) {
			return invalid(prefix+".id", "must be a canonical identifier")
		}
		if _, exists := seen[item.ID]; exists {
			return invalid(field, "contains duplicate "+item.ID)
		}
		seen[item.ID] = struct{}{}
		if !validEvidenceKind(item.Kind) {
			return invalid(prefix+".kind", "unsupported evidence kind")
		}
		if strings.TrimSpace(item.Reference) == "" || strings.TrimSpace(item.Reference) != item.Reference || len(item.Reference) > 512 {
			return invalid(prefix+".reference", "must be a bounded opaque reference")
		}
		if !digestPattern.MatchString(item.Digest) {
			return invalid(prefix+".digest", "must be a sha256 digest")
		}
		if err := validateTimestamp(prefix+".recorded_at", item.RecordedAt); err != nil {
			return err
		}
	}
	return nil
}

func containsEvidenceKind(evidence []EvidenceReference, kind EvidenceKind) bool {
	for _, item := range evidence {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func validateFailure(field string, failure FailureMetadata) error {
	if !errorCodePattern.MatchString(failure.Code) {
		return invalid(field+".code", "must be an uppercase stable error code")
	}
	if strings.TrimSpace(failure.Message) == "" || strings.TrimSpace(failure.Message) != failure.Message || len(failure.Message) > 512 {
		return invalid(field+".message", "must be non-empty, canonical, and bounded")
	}
	return nil
}

func validateTimestamp(field string, value time.Time) error {
	if value.IsZero() {
		return invalid(field, "must not be zero")
	}
	_, offset := value.Zone()
	if offset != 0 {
		return invalid(field, "must be UTC")
	}
	return nil
}

func validateOptionalTimestamp(field string, value *time.Time) error {
	if value == nil {
		return nil
	}
	return validateTimestamp(field, *value)
}

func providerHas(provider ProviderMetadata, capability ProviderCapability) bool {
	for _, candidate := range provider.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func validObjectKind(value ObjectKind) bool {
	switch value {
	case ObjectNode, ObjectService, ObjectDatabase, ObjectConfiguration, ObjectUser, ObjectGroup, ObjectPolicy, ObjectApplication, ObjectSite, ObjectControlPlane:
		return true
	default:
		return false
	}
}

func validRecoveryPointState(value RecoveryPointState) bool {
	switch value {
	case RecoveryPointCreating, RecoveryPointReady, RecoveryPointPartial, RecoveryPointFailed, RecoveryPointExpired:
		return true
	default:
		return false
	}
}

func validRecoveryPointTrigger(value RecoveryPointTrigger) bool {
	switch value {
	case TriggerManual, TriggerScheduled, TriggerPreChange, TriggerPreDestructive:
		return true
	default:
		return false
	}
}

func validConsistency(value ConsistencyLevel) bool {
	switch value {
	case ConsistencyCrash, ConsistencyApplication, ConsistencyObject:
		return true
	default:
		return false
	}
}

func validProviderCapability(value ProviderCapability) bool {
	switch value {
	case CapabilityBackup, CapabilityRestore, CapabilityBaseBackup, CapabilityWALArchive, CapabilityPITR, CapabilitySnapshot, CapabilityObjectVersion, CapabilityObjectRestore, CapabilityRestoreDrill, CapabilityFencing:
		return true
	default:
		return false
	}
}

func validEvidenceKind(value EvidenceKind) bool {
	switch value {
	case EvidenceChecksum, EvidenceProviderLog, EvidenceRestoreDrill, EvidenceFunctionalTest, EvidenceFencingConfirmation:
		return true
	default:
		return false
	}
}

func validBackupMode(value BackupMode) bool {
	switch value {
	case BackupFull, BackupIncremental, BackupDifferential, BackupSnapshot, BackupBaseWAL, BackupObjectVersion:
		return true
	default:
		return false
	}
}

func validBackupState(value BackupState) bool {
	switch value {
	case BackupPlanned, BackupRunning, BackupCompleted, BackupFailed, BackupExpired, BackupDeleted:
		return true
	default:
		return false
	}
}

func validBackupHealthState(value BackupHealthState) bool {
	switch value {
	case BackupHealthUnknown, BackupHealthUnverified, BackupHealthVerified, BackupHealthDegraded, BackupHealthFailed:
		return true
	default:
		return false
	}
}

func validRestoreMode(value RestoreMode) bool {
	switch value {
	case RestoreInPlace, RestoreAlternate, RestoreIsolatedDrill, RestoreObjectLevel, RestorePITR:
		return true
	default:
		return false
	}
}

func validRestoreState(value RestoreState) bool {
	switch value {
	case RestorePlanned, RestoreRunning, RestoreVerifying, RestoreSucceeded, RestoreFailed, RestoreCancelled:
		return true
	default:
		return false
	}
}
