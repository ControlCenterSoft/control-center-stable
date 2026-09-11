package recovery

import (
	"errors"
	"fmt"
	"time"
)

// RecoveryMetadataGraph is one complete, read-consistent recovery metadata
// snapshot. Callers must populate every slice, using an empty (non-nil) slice
// when that record type has no objects. A nil slice is treated as an
// unavailable projection and fails closed.
//
// The graph contains metadata only. Validating it never invokes a provider,
// reads an artifact, performs fencing, or changes infrastructure.
type RecoveryMetadataGraph struct {
	RecoveryPoints    []RecoveryPoint
	Backups           []BackupMetadata
	Restores          []RestoreMetadata
	ObjectiveEvidence []RecoveryObjectiveEvidence
}

type evidenceOrigin struct {
	restoreID string
	channel   string
	evidence  EvidenceReference
	path      string
}

// ValidateRecoveryMetadataGraph validates references and provenance that
// cannot be proven by the validators for individual metadata records. The
// supplied graph must be a complete snapshot; validating a filtered or
// partially unavailable projection would turn missing records into ambiguous
// trust decisions and is therefore rejected.
func ValidateRecoveryMetadataGraph(graph RecoveryMetadataGraph) error {
	for _, collection := range []struct {
		name    string
		missing bool
	}{
		{name: "recovery_points", missing: graph.RecoveryPoints == nil},
		{name: "backups", missing: graph.Backups == nil},
		{name: "restores", missing: graph.Restores == nil},
		{name: "objective_evidence", missing: graph.ObjectiveEvidence == nil},
	} {
		if collection.missing {
			return invalid(collection.name, "must be a complete non-nil snapshot")
		}
	}

	points := make(map[string]int, len(graph.RecoveryPoints))
	backups := make(map[string]int, len(graph.Backups))
	restores := make(map[string]int, len(graph.Restores))
	objectIDs := make(map[string]string, len(graph.RecoveryPoints)+len(graph.Backups)+len(graph.Restores)+len(graph.ObjectiveEvidence))
	providerOperations := make(map[string]string)
	artifacts := make(map[string]string)

	for index := range graph.RecoveryPoints {
		path := fmt.Sprintf("recovery_points[%d]", index)
		point := graph.RecoveryPoints[index]
		if err := ValidateRecoveryPoint(point); err != nil {
			return prefixedValidationError(path, err)
		}
		if err := registerRecoveryObjectID(objectIDs, path, point.ObjectID); err != nil {
			return err
		}
		points[point.ObjectID] = index
	}

	for index := range graph.Backups {
		path := fmt.Sprintf("backups[%d]", index)
		backup := graph.Backups[index]
		if err := ValidateBackupMetadata(backup); err != nil {
			return prefixedValidationError(path, err)
		}
		if err := registerRecoveryObjectID(objectIDs, path, backup.ObjectID); err != nil {
			return err
		}
		backups[backup.ObjectID] = index
		if err := registerProviderOperation(providerOperations, path+".provider", backup.Provider); err != nil {
			return err
		}
		if backup.Artifact != nil {
			key := backup.Artifact.RepositoryID + "\x00" + backup.Artifact.ObjectKey
			if previous, exists := artifacts[key]; exists {
				return invalid(path+".artifact.object_key", "artifact is already claimed by "+previous)
			}
			artifacts[key] = path
		}
	}

	evidenceOrigins := make(map[string]evidenceOrigin)
	for index := range graph.Restores {
		path := fmt.Sprintf("restores[%d]", index)
		restore := graph.Restores[index]
		if err := ValidateRestoreMetadata(restore); err != nil {
			return prefixedValidationError(path, err)
		}
		if err := registerRecoveryObjectID(objectIDs, path, restore.ObjectID); err != nil {
			return err
		}
		restores[restore.ObjectID] = index
		if err := registerProviderOperation(providerOperations, path+".provider", restore.Provider); err != nil {
			return err
		}
		if restore.Fencing.Provider != nil {
			if err := registerProviderOperation(providerOperations, path+".fencing.provider", *restore.Fencing.Provider); err != nil {
				return err
			}
		}
		if err := registerEvidenceOrigins(evidenceOrigins, path+".verification.evidence", restore.ObjectID, "verification", restore.Verification.Evidence); err != nil {
			return err
		}
		if err := registerEvidenceOrigins(evidenceOrigins, path+".fencing.evidence", restore.ObjectID, "fencing", restore.Fencing.Evidence); err != nil {
			return err
		}
	}

	for index := range graph.ObjectiveEvidence {
		path := fmt.Sprintf("objective_evidence[%d]", index)
		objective := graph.ObjectiveEvidence[index]
		if err := ValidateRecoveryObjectiveEvidence(objective); err != nil {
			return prefixedValidationError(path, err)
		}
		if err := registerRecoveryObjectID(objectIDs, path, objective.ObjectID); err != nil {
			return err
		}
	}

	if err := validateRecoveryPointLinks(graph, points, backups); err != nil {
		return err
	}
	if err := validateBackupParentCycles(graph.Backups, backups); err != nil {
		return err
	}
	if err := validateBackupParentLinks(graph, backups); err != nil {
		return err
	}
	if err := validateRestoreLinks(graph, points, backups); err != nil {
		return err
	}
	if err := validateBackupHealthLinks(graph, restores, evidenceOrigins); err != nil {
		return err
	}
	return validateObjectiveLinks(graph, points, backups, restores, evidenceOrigins)
}

func registerRecoveryObjectID(seen map[string]string, path, objectID string) error {
	if previous, exists := seen[objectID]; exists {
		return invalid(path+".object_id", "duplicates recovery object id claimed by "+previous)
	}
	seen[objectID] = path
	return nil
}

func registerProviderOperation(seen map[string]string, path string, provider ProviderMetadata) error {
	if provider.OperationID == "" {
		return nil
	}
	key := provider.ProviderID + "\x00" + provider.AdapterID + "\x00" + provider.OperationID
	if previous, exists := seen[key]; exists {
		return invalid(path+".operation_id", "provider operation is already claimed by "+previous)
	}
	seen[key] = path
	return nil
}

func registerEvidenceOrigins(origins map[string]evidenceOrigin, path, restoreID, channel string, evidence []EvidenceReference) error {
	for index, item := range evidence {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if previous, exists := origins[item.ID]; exists {
			return invalid(itemPath+".id", "evidence id is already claimed by "+previous.path)
		}
		origins[item.ID] = evidenceOrigin{restoreID: restoreID, channel: channel, evidence: item, path: itemPath}
	}
	return nil
}

func validateRecoveryPointLinks(graph RecoveryMetadataGraph, points, backups map[string]int) error {
	for pointIndex := range graph.RecoveryPoints {
		point := graph.RecoveryPoints[pointIndex]
		path := fmt.Sprintf("recovery_points[%d]", pointIndex)
		covered := make(map[ObjectReference]bool, len(point.Objects))
		for backupIndex, backupID := range point.BackupIDs {
			index, exists := backups[backupID]
			if !exists {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupIndex), "referenced backup does not exist")
			}
			backup := graph.Backups[index]
			if backup.RecoveryPointID != point.ObjectID {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupIndex), "backup points to a different recovery point")
			}
			if err := validateLinkedOwnership(path+".backup_ids", point.ScopeID, point.OwnerScope, backup.ScopeID, backup.OwnerScope); err != nil {
				return err
			}
			if !containsObjectReference(point.Objects, backup.Target) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupIndex), "backup target is outside the recovery point")
			}
			if !validReferencedBackupState(point.State, backup.State) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupIndex), "backup state is not coherent with recovery point state")
			}
			if backup.RequestedAt.Before(point.CreatedAt) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupIndex), "backup request predates recovery point creation")
			}
			if backup.CompletedAt != nil && (point.State == RecoveryPointReady || point.State == RecoveryPointPartial || point.State == RecoveryPointExpired) && backup.CompletedAt.After(point.UpdatedAt) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupIndex), "backup completion follows recovery point updated_at")
			}
			covered[backup.Target] = true
		}
		if point.State == RecoveryPointReady {
			for objectIndex, object := range point.Objects {
				if !covered[object] {
					return invalid(fmt.Sprintf("%s.objects[%d]", path, objectIndex), "ready recovery point has no completed backup for this object")
				}
			}
		}
	}

	for backupIndex := range graph.Backups {
		backup := graph.Backups[backupIndex]
		path := fmt.Sprintf("backups[%d]", backupIndex)
		pointIndex, exists := points[backup.RecoveryPointID]
		if !exists {
			return invalid(path+".recovery_point_id", "referenced recovery point does not exist")
		}
		point := graph.RecoveryPoints[pointIndex]
		if err := validateLinkedOwnership(path+".recovery_point_id", backup.ScopeID, backup.OwnerScope, point.ScopeID, point.OwnerScope); err != nil {
			return err
		}
		if !containsObjectReference(point.Objects, backup.Target) {
			return invalid(path+".target", "target is outside the referenced recovery point")
		}
		if backup.RequestedAt.Before(point.CreatedAt) || backup.CreatedAt.Before(point.CreatedAt) {
			return invalid(path+".requested_at", "backup provenance predates recovery point creation")
		}
		listed := containsID(point.BackupIDs, backup.ObjectID)
		if successfulBackupState(backup.State) && !listed {
			return invalid(path+".object_id", "successful backup is not referenced by its recovery point")
		}
		if !successfulBackupState(backup.State) && listed {
			return invalid(path+".object_id", "non-successful backup cannot be referenced as recovery point output")
		}
		switch backup.State {
		case BackupPlanned, BackupRunning:
			if point.State != RecoveryPointCreating {
				return invalid(path+".state", "active backup requires a CREATING recovery point")
			}
		case BackupFailed:
			if point.State != RecoveryPointCreating && point.State != RecoveryPointPartial && point.State != RecoveryPointFailed {
				return invalid(path+".state", "failed backup requires a CREATING, PARTIAL, or FAILED recovery point")
			}
		}
	}
	return nil
}

func validateBackupParentCycles(items []BackupMetadata, indexes map[string]int) error {
	const (
		visiting = iota + 1
		visited
	)
	states := make(map[string]int, len(items))
	var visit func(string) error
	visit = func(id string) error {
		if states[id] == visiting {
			return invalid("backups.parent_backup_id", "backup parent cycle detected at "+id)
		}
		if states[id] == visited {
			return nil
		}
		states[id] = visiting
		backup := items[indexes[id]]
		if backup.ParentBackupID != "" {
			if _, exists := indexes[backup.ParentBackupID]; !exists {
				return invalid(fmt.Sprintf("backups[%d].parent_backup_id", indexes[id]), "referenced parent backup does not exist")
			}
			if err := visit(backup.ParentBackupID); err != nil {
				return err
			}
		}
		states[id] = visited
		return nil
	}
	for index := range items {
		if err := visit(items[index].ObjectID); err != nil {
			return err
		}
	}
	return nil
}

func validateBackupParentLinks(graph RecoveryMetadataGraph, backups map[string]int) error {
	for index := range graph.Backups {
		backup := graph.Backups[index]
		if backup.ParentBackupID == "" {
			continue
		}
		path := fmt.Sprintf("backups[%d].parent_backup_id", index)
		parentIndex, exists := backups[backup.ParentBackupID]
		if !exists {
			return invalid(path, "referenced parent backup does not exist")
		}
		parent := graph.Backups[parentIndex]
		if err := validateLinkedOwnership(path, backup.ScopeID, backup.OwnerScope, parent.ScopeID, parent.OwnerScope); err != nil {
			return err
		}
		if parent.Target != backup.Target {
			return invalid(path, "parent backup has a different target")
		}
		if !sameProviderStorage(parent.Provider, backup.Provider) {
			return invalid(path, "parent backup has different provider or repository provenance")
		}
		if (parent.State != BackupCompleted && parent.State != BackupExpired) || parent.Artifact == nil || parent.CompletedAt == nil {
			return invalid(path, "parent backup is not an available completed artifact")
		}
		if parent.CompletedAt.After(backup.RequestedAt) {
			return invalid(path, "parent backup completed after child request")
		}
		if parent.DataCapturedAt != nil && backup.DataCapturedAt != nil && parent.DataCapturedAt.After(*backup.DataCapturedAt) {
			return invalid(path, "parent data capture follows child data capture")
		}
		if backup.Mode == BackupDifferential && parent.Mode != BackupFull {
			return invalid(path, "differential backup requires a FULL parent")
		}
		if backup.Mode == BackupIncremental && parent.Mode != BackupFull && parent.Mode != BackupIncremental {
			return invalid(path, "incremental backup requires a FULL or INCREMENTAL parent")
		}
	}
	return nil
}

func validateRestoreLinks(graph RecoveryMetadataGraph, points, backups map[string]int) error {
	for restoreIndex := range graph.Restores {
		restore := graph.Restores[restoreIndex]
		path := fmt.Sprintf("restores[%d]", restoreIndex)
		pointIndex, exists := points[restore.RecoveryPointID]
		if !exists {
			return invalid(path+".recovery_point_id", "referenced recovery point does not exist")
		}
		point := graph.RecoveryPoints[pointIndex]
		if err := validateLinkedOwnership(path+".recovery_point_id", restore.ScopeID, restore.OwnerScope, point.ScopeID, point.OwnerScope); err != nil {
			return err
		}
		if point.State == RecoveryPointCreating || point.State == RecoveryPointFailed {
			return invalid(path+".recovery_point_id", "restore requires a completed recovery point")
		}
		if restore.RequestedAt.Before(point.CreatedAt) || restore.CreatedAt.Before(point.CreatedAt) {
			return invalid(path+".requested_at", "restore provenance predates recovery point creation")
		}
		if point.ExpiresAt != nil && !restore.RequestedAt.Before(*point.ExpiresAt) {
			return invalid(path+".requested_at", "restore request does not precede recovery point expiration")
		}

		sources := make([]BackupMetadata, 0, len(restore.BackupIDs))
		for backupPosition, backupID := range restore.BackupIDs {
			backupIndex, exists := backups[backupID]
			if !exists {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupPosition), "referenced backup does not exist")
			}
			backup := graph.Backups[backupIndex]
			if backup.RecoveryPointID != point.ObjectID || !containsID(point.BackupIDs, backup.ObjectID) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupPosition), "backup is not an output of the referenced recovery point")
			}
			if err := validateLinkedOwnership(path+".backup_ids", restore.ScopeID, restore.OwnerScope, backup.ScopeID, backup.OwnerScope); err != nil {
				return err
			}
			if (backup.State != BackupCompleted && backup.State != BackupExpired) || backup.Artifact == nil || backup.CompletedAt == nil {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupPosition), "backup has no available completed artifact")
			}
			if backup.CompletedAt.After(restore.RequestedAt) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupPosition), "restore request predates backup completion")
			}
			if !sameProviderStorage(backup.Provider, restore.Provider) {
				return invalid(fmt.Sprintf("%s.backup_ids[%d]", path, backupPosition), "restore provider does not match backup artifact provenance")
			}
			sources = append(sources, backup)
		}

		switch restore.Mode {
		case RestoreInPlace, RestorePITR, RestoreObjectLevel:
			if !containsObjectReference(point.Objects, restore.Target) || !backupTargetsContain(sources, restore.Target) {
				return invalid(path+".target", "restore target is not covered by its recovery point backups")
			}
		case RestoreAlternate, RestoreIsolatedDrill:
			if !backupTargetsContainType(sources, restore.Target) {
				return invalid(path+".target", "alternate/drill target type is not compatible with source backups")
			}
		}
		for requestedIndex, requested := range restore.RequestedObjects {
			if !containsObjectReference(point.Objects, requested) {
				return invalid(fmt.Sprintf("%s.requested_objects[%d]", path, requestedIndex), "requested object is outside the recovery point")
			}
			if !backupTargetsContain(sources, requested) {
				return invalid(fmt.Sprintf("%s.requested_objects[%d]", path, requestedIndex), "requested object is not covered by referenced backups")
			}
		}
		if restore.Mode == RestorePITR {
			covered := false
			for _, backup := range sources {
				if backup.Mode == BackupBaseWAL && backup.PITR != nil && restore.PITRTargetAt != nil && !restore.PITRTargetAt.Before(backup.PITR.EarliestRestoreAt) && !restore.PITRTargetAt.After(backup.PITR.LatestRestoreAt) {
					covered = true
					break
				}
			}
			if !covered {
				return invalid(path+".pitr_target_at", "no referenced BASE_WAL backup covers the PITR target")
			}
		}
		if restore.StartedAt != nil {
			for evidenceIndex, evidence := range restore.Verification.Evidence {
				if evidence.RecordedAt.Before(*restore.StartedAt) {
					return invalid(fmt.Sprintf("%s.verification.evidence[%d].recorded_at", path, evidenceIndex), "verification evidence predates restore start")
				}
			}
		}
	}
	return nil
}

func validateBackupHealthLinks(graph RecoveryMetadataGraph, restores map[string]int, origins map[string]evidenceOrigin) error {
	for backupIndex := range graph.Backups {
		backup := graph.Backups[backupIndex]
		if backup.Health.State != BackupHealthVerified && backup.Health.State != BackupHealthDegraded {
			continue
		}
		path := fmt.Sprintf("backups[%d].health", backupIndex)
		restoreIndex, exists := restores[backup.Health.RestoreID]
		if !exists {
			return invalid(path+".restore_id", "referenced restore does not exist")
		}
		restore := graph.Restores[restoreIndex]
		if err := validateLinkedOwnership(path+".restore_id", backup.ScopeID, backup.OwnerScope, restore.ScopeID, restore.OwnerScope); err != nil {
			return err
		}
		if restore.RecoveryPointID != backup.RecoveryPointID || !containsID(restore.BackupIDs, backup.ObjectID) {
			return invalid(path+".restore_id", "restore does not prove this backup and recovery point")
		}
		if restore.Mode != RestoreIsolatedDrill || restore.CompletedAt == nil || restore.Verification.VerifiedAt == nil {
			return invalid(path+".restore_id", "backup health requires a completed isolated restore drill")
		}
		if backup.Health.LastVerifiedAt.Before(*restore.CompletedAt) || backup.Health.LastVerifiedAt.Before(*restore.Verification.VerifiedAt) {
			return invalid(path+".last_verified_at", "must not precede linked restore completion and verification")
		}
		if err := validateEvidenceConsumers(path+".evidence", backup.Health.Evidence, restore.ObjectID, "verification", origins); err != nil {
			return err
		}
		if backup.Health.State == BackupHealthVerified {
			if restore.State != RestoreSucceeded || restore.Verification.Outcome != VerificationPassed {
				return invalid(path+".state", "VERIFIED requires a successful linked restore")
			}
		} else if restore.State != RestoreFailed || restore.Verification.Outcome != VerificationFailed {
			return invalid(path+".state", "DEGRADED requires a failed linked restore verification")
		}
	}
	return nil
}

func validateObjectiveLinks(graph RecoveryMetadataGraph, points, backups, restores map[string]int, origins map[string]evidenceOrigin) error {
	for objectiveIndex := range graph.ObjectiveEvidence {
		objective := graph.ObjectiveEvidence[objectiveIndex]
		path := fmt.Sprintf("objective_evidence[%d]", objectiveIndex)
		pointIndex, pointExists := points[objective.RecoveryPointID]
		if !pointExists {
			return invalid(path+".recovery_point_id", "referenced recovery point does not exist")
		}
		restoreIndex, restoreExists := restores[objective.RestoreID]
		if !restoreExists {
			return invalid(path+".restore_id", "referenced restore does not exist")
		}
		point := graph.RecoveryPoints[pointIndex]
		restore := graph.Restores[restoreIndex]
		if err := validateLinkedOwnership(path+".recovery_point_id", objective.ScopeID, objective.OwnerScope, point.ScopeID, point.OwnerScope); err != nil {
			return err
		}
		if err := validateLinkedOwnership(path+".restore_id", objective.ScopeID, objective.OwnerScope, restore.ScopeID, restore.OwnerScope); err != nil {
			return err
		}
		if restore.RecoveryPointID != point.ObjectID {
			return invalid(path+".restore_id", "restore belongs to a different recovery point")
		}
		if restore.Mode != RestoreIsolatedDrill || restore.CompletedAt == nil || restore.Verification.VerifiedAt == nil {
			return invalid(path+".restore_id", "objective evidence requires a completed isolated restore drill")
		}
		if !containsObjectReference(point.Objects, objective.Target) || !restoreCoversObjectiveTarget(graph, backups, restore, objective.Target) {
			return invalid(path+".target", "target is not covered by the linked recovery point and restore")
		}
		if objective.MeasuredAt.Before(*restore.CompletedAt) || objective.MeasuredAt.Before(*restore.Verification.VerifiedAt) {
			return invalid(path+".measured_at", "must not precede linked restore completion and verification")
		}
		if objective.CreatedAt.Before(objective.MeasuredAt) {
			return invalid(path+".created_at", "objective record cannot be created before its measurement")
		}
		if err := validateEvidenceConsumers(path+".evidence", objective.Evidence, restore.ObjectID, "verification", origins); err != nil {
			return err
		}
		switch objective.Result {
		case ObjectivePassed:
			if restore.State != RestoreSucceeded || restore.Verification.Outcome != VerificationPassed {
				return invalid(path+".result", "PASSED requires a successful linked restore")
			}
		case ObjectiveFailed:
			if restore.State != RestoreSucceeded && restore.State != RestoreFailed {
				return invalid(path+".result", "FAILED requires a terminal linked restore")
			}
		}
		if objective.ObservedRTOSeconds != nil && restore.StartedAt != nil {
			minimum, representable := elapsedSecondsCeiling(*restore.StartedAt, *restore.CompletedAt)
			if !representable {
				return invalid(path+".observed_rto_seconds", "linked restore execution duration exceeds representable seconds")
			}
			if *objective.ObservedRTOSeconds < minimum {
				return invalid(path+".observed_rto_seconds", "understates the linked restore execution duration")
			}
		}
	}
	return nil
}

func validateEvidenceConsumers(path string, evidence []EvidenceReference, restoreID, channel string, origins map[string]evidenceOrigin) error {
	for index, item := range evidence {
		origin, exists := origins[item.ID]
		if !exists {
			return invalid(fmt.Sprintf("%s[%d].id", path, index), "evidence has no restore provenance")
		}
		if origin.restoreID != restoreID || origin.channel != channel {
			return invalid(fmt.Sprintf("%s[%d].id", path, index), "evidence belongs to a different restore provenance")
		}
		if !sameEvidence(origin.evidence, item) {
			return invalid(fmt.Sprintf("%s[%d]", path, index), "evidence metadata differs from its immutable origin")
		}
	}
	return nil
}

func validateLinkedOwnership(path, leftScope, leftOwner, rightScope, rightOwner string) error {
	if leftScope != rightScope {
		return invalid(path, "linked records have different scope_id")
	}
	if leftOwner != rightOwner {
		return invalid(path, "linked records have different owner_scope")
	}
	return nil
}

func prefixedValidationError(prefix string, err error) error {
	var validation *ValidationError
	if errors.As(err, &validation) {
		return invalid(prefix+"."+validation.Field, validation.Message)
	}
	return invalid(prefix, err.Error())
}

func successfulBackupState(state BackupState) bool {
	return state == BackupCompleted || state == BackupExpired || state == BackupDeleted
}

func validReferencedBackupState(pointState RecoveryPointState, backupState BackupState) bool {
	switch pointState {
	case RecoveryPointCreating, RecoveryPointReady, RecoveryPointPartial:
		return backupState == BackupCompleted
	case RecoveryPointExpired:
		return successfulBackupState(backupState)
	default:
		return false
	}
}

func containsID(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsObjectReference(values []ObjectReference, wanted ObjectReference) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func backupTargetsContain(backups []BackupMetadata, target ObjectReference) bool {
	for _, backup := range backups {
		if backup.Target == target {
			return true
		}
	}
	return false
}

func backupTargetsContainType(backups []BackupMetadata, target ObjectReference) bool {
	for _, backup := range backups {
		if backup.Target.Kind == target.Kind && backup.Target.Statefulness == target.Statefulness {
			return true
		}
	}
	return false
}

func sameProviderStorage(left, right ProviderMetadata) bool {
	return left.ProviderID == right.ProviderID && left.AdapterID == right.AdapterID && left.Version == right.Version && left.RepositoryID == right.RepositoryID
}

func sameEvidence(left, right EvidenceReference) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.Reference == right.Reference && left.Digest == right.Digest && left.RecordedAt.Equal(right.RecordedAt)
}

func restoreCoversObjectiveTarget(graph RecoveryMetadataGraph, backups map[string]int, restore RestoreMetadata, target ObjectReference) bool {
	for _, backupID := range restore.BackupIDs {
		if graph.Backups[backups[backupID]].Target == target {
			return true
		}
	}
	return containsObjectReference(restore.RequestedObjects, target)
}

func elapsedSecondsCeiling(start, end time.Time) (uint64, bool) {
	if end.Before(start) {
		return 0, false
	}

	// Bias signed Unix seconds into their natural unsigned order before
	// subtracting. time.Time.Sub saturates at roughly 292 years, which could
	// otherwise let an objective understate a longer restore duration.
	const signBit = uint64(1) << 63
	startSeconds := uint64(start.Unix()) ^ signBit
	endSeconds := uint64(end.Unix()) ^ signBit
	seconds := endSeconds - startSeconds
	if end.Nanosecond() > start.Nanosecond() {
		if seconds == ^uint64(0) {
			return 0, false
		}
		seconds++
	}
	return seconds, true
}
