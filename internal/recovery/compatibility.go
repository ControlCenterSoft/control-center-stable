package recovery

import (
	"fmt"
	"sort"
)

const V03ProductVersion = "0.3.x"

type LegacyRecordKind string

const (
	LegacyJob            LegacyRecordKind = "JOB"
	LegacyResource       LegacyRecordKind = "RESOURCE"
	LegacyConfigRevision LegacyRecordKind = "CONFIG_REVISION"
)

type LegacyV03Record struct {
	Kind LegacyRecordKind `json:"kind"`
	ID   string           `json:"id"`
}

type LegacyDisposition string

const LegacyRequiresReview LegacyDisposition = "REQUIRES_REVIEW"

type QuarantinedLegacyRecord struct {
	Kind        LegacyRecordKind  `json:"kind"`
	ID          string            `json:"id"`
	Disposition LegacyDisposition `json:"disposition"`
	Reason      string            `json:"reason"`
}

// V03MigrationResult intentionally contains typed output collections. They are
// empty because 0.3.x had no typed recovery evidence that can be promoted
// safely. Generic legacy record references are retained for explicit review.
type V03MigrationResult struct {
	SchemaVersion        string                      `json:"schema_version"`
	SourceProductVersion string                      `json:"source_product_version"`
	RecoveryPoints       []RecoveryPoint             `json:"recovery_points"`
	Backups              []BackupMetadata            `json:"backups"`
	Restores             []RestoreMetadata           `json:"restores"`
	ObjectiveEvidence    []RecoveryObjectiveEvidence `json:"objective_evidence"`
	Quarantined          []QuarantinedLegacyRecord   `json:"quarantined"`
	Warnings             []string                    `json:"warnings"`
}

// MigrateV03Metadata is a deterministic compatibility boundary, not an
// execution path. It never turns a generic successful Job or Resource into a
// backup, a recovery point, or proof of recoverability.
func MigrateV03Metadata(records []LegacyV03Record) (V03MigrationResult, error) {
	seen := make(map[string]struct{}, len(records))
	quarantined := make([]QuarantinedLegacyRecord, 0, len(records))
	for index, record := range records {
		if !validLegacyRecordKind(record.Kind) {
			return V03MigrationResult{}, invalid(fmt.Sprintf("records[%d].kind", index), "unsupported 0.3 record kind")
		}
		if !identifierPattern.MatchString(record.ID) {
			return V03MigrationResult{}, invalid(fmt.Sprintf("records[%d].id", index), "must be a canonical identifier")
		}
		key := string(record.Kind) + "\x00" + record.ID
		if _, exists := seen[key]; exists {
			return V03MigrationResult{}, invalid("records", "contains a duplicate legacy record")
		}
		seen[key] = struct{}{}
		quarantined = append(quarantined, QuarantinedLegacyRecord{
			Kind: record.Kind, ID: record.ID, Disposition: LegacyRequiresReview,
			Reason: "0.3.x record has no typed recovery or restore-verification evidence",
		})
	}
	sort.Slice(quarantined, func(i, j int) bool {
		if quarantined[i].Kind == quarantined[j].Kind {
			return quarantined[i].ID < quarantined[j].ID
		}
		return quarantined[i].Kind < quarantined[j].Kind
	})
	warnings := []string{
		"0.3.x generic job success is not backup-health or restore evidence",
		"0.3.x has no typed RecoveryPoint, BackupMetadata, RestoreMetadata, or RPO/RTO evidence objects",
	}
	if len(quarantined) > 0 {
		warnings = append(warnings, "legacy generic records require explicit operator mapping or archival")
	}
	return V03MigrationResult{
		SchemaVersion:        V03CompatibilitySchemaVersion,
		SourceProductVersion: V03ProductVersion,
		RecoveryPoints:       []RecoveryPoint{},
		Backups:              []BackupMetadata{},
		Restores:             []RestoreMetadata{},
		ObjectiveEvidence:    []RecoveryObjectiveEvidence{},
		Quarantined:          quarantined,
		Warnings:             warnings,
	}, nil
}

func validLegacyRecordKind(kind LegacyRecordKind) bool {
	switch kind {
	case LegacyJob, LegacyResource, LegacyConfigRevision:
		return true
	default:
		return false
	}
}
