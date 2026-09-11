package recovery

import (
	"time"

	"control-center/internal/corecontracts"
)

const (
	RecoveryPointSchemaVersion     = "recovery.point/v1"
	BackupMetadataSchemaVersion    = "recovery.backup/v1"
	RestoreMetadataSchemaVersion   = "recovery.restore/v1"
	ObjectiveEvidenceSchemaVersion = "recovery.objective-evidence/v1"
	V03CompatibilitySchemaVersion  = "recovery.v03-compatibility/v1"
)

type ObjectKind string

const (
	ObjectNode          ObjectKind = "NODE"
	ObjectService       ObjectKind = "SERVICE"
	ObjectDatabase      ObjectKind = "DATABASE"
	ObjectConfiguration ObjectKind = "CONFIGURATION"
	ObjectUser          ObjectKind = "USER"
	ObjectGroup         ObjectKind = "GROUP"
	ObjectPolicy        ObjectKind = "POLICY"
	ObjectApplication   ObjectKind = "APPLICATION"
	ObjectSite          ObjectKind = "SITE"
	ObjectControlPlane  ObjectKind = "CONTROL_PLANE"
)

type Statefulness string

const (
	Stateless Statefulness = "STATELESS"
	Stateful  Statefulness = "STATEFUL"
)

type ObjectReference struct {
	Kind         ObjectKind   `json:"kind"`
	ObjectID     string       `json:"object_id"`
	ScopeID      string       `json:"scope_id"`
	Statefulness Statefulness `json:"statefulness"`
}

type FailureMetadata struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type RecoveryPointState string

const (
	RecoveryPointCreating RecoveryPointState = "CREATING"
	RecoveryPointReady    RecoveryPointState = "READY"
	RecoveryPointPartial  RecoveryPointState = "PARTIAL"
	RecoveryPointFailed   RecoveryPointState = "FAILED"
	RecoveryPointExpired  RecoveryPointState = "EXPIRED"
)

type RecoveryPointTrigger string

const (
	TriggerManual         RecoveryPointTrigger = "MANUAL"
	TriggerScheduled      RecoveryPointTrigger = "SCHEDULED"
	TriggerPreChange      RecoveryPointTrigger = "PRE_CHANGE"
	TriggerPreDestructive RecoveryPointTrigger = "PRE_DESTRUCTIVE"
)

type ConsistencyLevel string

const (
	ConsistencyCrash       ConsistencyLevel = "CRASH_CONSISTENT"
	ConsistencyApplication ConsistencyLevel = "APPLICATION_CONSISTENT"
	ConsistencyObject      ConsistencyLevel = "OBJECT_VERSIONED"
)

// RecoveryPoint groups immutable backup/object-version metadata for one
// consistency boundary. It contains no execution instructions.
type RecoveryPoint struct {
	corecontracts.ObjectMetadata
	SchemaVersion string               `json:"schema_version"`
	State         RecoveryPointState   `json:"state"`
	Trigger       RecoveryPointTrigger `json:"trigger"`
	Consistency   ConsistencyLevel     `json:"consistency"`
	Objects       []ObjectReference    `json:"objects"`
	BackupIDs     []string             `json:"backup_ids"`
	ChangeID      string               `json:"change_id,omitempty"`
	ExpiresAt     *time.Time           `json:"expires_at,omitempty"`
	Failure       *FailureMetadata     `json:"failure,omitempty"`
}

type ProviderCapability string

const (
	CapabilityBackup        ProviderCapability = "BACKUP"
	CapabilityRestore       ProviderCapability = "RESTORE"
	CapabilityBaseBackup    ProviderCapability = "BASE_BACKUP"
	CapabilityWALArchive    ProviderCapability = "WAL_ARCHIVE"
	CapabilityPITR          ProviderCapability = "PITR"
	CapabilitySnapshot      ProviderCapability = "SNAPSHOT"
	CapabilityObjectVersion ProviderCapability = "OBJECT_VERSION"
	CapabilityObjectRestore ProviderCapability = "OBJECT_RESTORE"
	CapabilityRestoreDrill  ProviderCapability = "RESTORE_DRILL"
	CapabilityFencing       ProviderCapability = "FENCING"
)

// ProviderMetadata identifies an adapter and an opaque provider operation.
// Credentials, tokens, endpoints, and executable configuration are excluded.
type ProviderMetadata struct {
	ProviderID   string               `json:"provider_id"`
	AdapterID    string               `json:"adapter_id"`
	Version      string               `json:"version"`
	RepositoryID string               `json:"repository_id,omitempty"`
	OperationID  string               `json:"operation_id,omitempty"`
	Capabilities []ProviderCapability `json:"capabilities"`
}

type EvidenceKind string

const (
	EvidenceChecksum            EvidenceKind = "CHECKSUM"
	EvidenceProviderLog         EvidenceKind = "PROVIDER_LOG"
	EvidenceRestoreDrill        EvidenceKind = "RESTORE_DRILL"
	EvidenceFunctionalTest      EvidenceKind = "FUNCTIONAL_TEST"
	EvidenceFencingConfirmation EvidenceKind = "FENCING_CONFIRMATION"
)

type EvidenceReference struct {
	ID         string       `json:"id"`
	Kind       EvidenceKind `json:"kind"`
	Reference  string       `json:"reference"`
	Digest     string       `json:"digest"`
	RecordedAt time.Time    `json:"recorded_at"`
}

type BackupMode string

const (
	BackupFull          BackupMode = "FULL"
	BackupIncremental   BackupMode = "INCREMENTAL"
	BackupDifferential  BackupMode = "DIFFERENTIAL"
	BackupSnapshot      BackupMode = "SNAPSHOT"
	BackupBaseWAL       BackupMode = "BASE_WAL"
	BackupObjectVersion BackupMode = "OBJECT_VERSION"
)

type BackupState string

const (
	BackupPlanned   BackupState = "PLANNED"
	BackupRunning   BackupState = "RUNNING"
	BackupCompleted BackupState = "COMPLETED"
	BackupFailed    BackupState = "FAILED"
	BackupExpired   BackupState = "EXPIRED"
	BackupDeleted   BackupState = "DELETED"
)

type EncryptionMode string

const (
	EncryptionNone            EncryptionMode = "NONE"
	EncryptionProviderManaged EncryptionMode = "PROVIDER_MANAGED"
	EncryptionCustomerManaged EncryptionMode = "CUSTOMER_MANAGED"
)

// BackupArtifact is a repository-relative immutable reference. ObjectKey may
// not be an absolute path or contain traversal segments.
type BackupArtifact struct {
	RepositoryID    string         `json:"repository_id"`
	ObjectKey       string         `json:"object_key"`
	SizeBytes       uint64         `json:"size_bytes"`
	Digest          string         `json:"digest"`
	Encryption      EncryptionMode `json:"encryption"`
	EncryptionKeyID string         `json:"encryption_key_id,omitempty"`
}

type PITRWindow struct {
	Timeline          uint32    `json:"timeline"`
	StartLSN          string    `json:"start_lsn"`
	EndLSN            string    `json:"end_lsn"`
	EarliestRestoreAt time.Time `json:"earliest_restore_at"`
	LatestRestoreAt   time.Time `json:"latest_restore_at"`
}

type BackupHealthState string

const (
	BackupHealthUnknown    BackupHealthState = "UNKNOWN"
	BackupHealthUnverified BackupHealthState = "UNVERIFIED"
	BackupHealthVerified   BackupHealthState = "VERIFIED"
	BackupHealthDegraded   BackupHealthState = "DEGRADED"
	BackupHealthFailed     BackupHealthState = "FAILED"
)

type BackupHealth struct {
	State          BackupHealthState   `json:"state"`
	LastVerifiedAt *time.Time          `json:"last_verified_at,omitempty"`
	RestoreID      string              `json:"restore_id,omitempty"`
	Evidence       []EvidenceReference `json:"evidence"`
}

// BackupMetadata records an observation about a backup operation and artifact.
// COMPLETED describes capture completion; recoverability is represented only by
// Health=VERIFIED with restore-drill evidence.
type BackupMetadata struct {
	corecontracts.ObjectMetadata
	SchemaVersion   string           `json:"schema_version"`
	RecoveryPointID string           `json:"recovery_point_id"`
	Target          ObjectReference  `json:"target"`
	Mode            BackupMode       `json:"mode"`
	State           BackupState      `json:"state"`
	ParentBackupID  string           `json:"parent_backup_id,omitempty"`
	Provider        ProviderMetadata `json:"provider"`
	Artifact        *BackupArtifact  `json:"artifact,omitempty"`
	PITR            *PITRWindow      `json:"pitr,omitempty"`
	RequestedAt     time.Time        `json:"requested_at"`
	StartedAt       *time.Time       `json:"started_at,omitempty"`
	DataCapturedAt  *time.Time       `json:"data_captured_at,omitempty"`
	CompletedAt     *time.Time       `json:"completed_at,omitempty"`
	Health          BackupHealth     `json:"health"`
	Failure         *FailureMetadata `json:"failure,omitempty"`
}

type FencingState string

const (
	FencingNotRequired FencingState = "NOT_REQUIRED"
	FencingRequired    FencingState = "REQUIRED"
	FencingConfirmed   FencingState = "CONFIRMED"
	FencingFailed      FencingState = "FAILED"
)

type FencingMetadata struct {
	State       FencingState        `json:"state"`
	TargetIDs   []string            `json:"target_ids"`
	Epoch       uint64              `json:"epoch"`
	Provider    *ProviderMetadata   `json:"provider,omitempty"`
	ConfirmedAt *time.Time          `json:"confirmed_at,omitempty"`
	Evidence    []EvidenceReference `json:"evidence"`
	Failure     *FailureMetadata    `json:"failure,omitempty"`
}

type RestoreMode string

const (
	RestoreInPlace       RestoreMode = "IN_PLACE"
	RestoreAlternate     RestoreMode = "ALTERNATE_TARGET"
	RestoreIsolatedDrill RestoreMode = "ISOLATED_DRILL"
	RestoreObjectLevel   RestoreMode = "OBJECT_LEVEL"
	RestorePITR          RestoreMode = "PITR"
)

type RestoreState string

const (
	RestorePlanned   RestoreState = "PLANNED"
	RestoreRunning   RestoreState = "RUNNING"
	RestoreVerifying RestoreState = "VERIFYING"
	RestoreSucceeded RestoreState = "SUCCEEDED"
	RestoreFailed    RestoreState = "FAILED"
	RestoreCancelled RestoreState = "CANCELLED"
)

type VerificationOutcome string

const (
	VerificationPending VerificationOutcome = "PENDING"
	VerificationPassed  VerificationOutcome = "PASSED"
	VerificationFailed  VerificationOutcome = "FAILED"
)

type RestoreVerification struct {
	Outcome    VerificationOutcome `json:"outcome"`
	VerifiedAt *time.Time          `json:"verified_at,omitempty"`
	Evidence   []EvidenceReference `json:"evidence"`
}

// RestoreMetadata is descriptive state only. Creating it does not execute a
// restore, fence a node, read an artifact, or alter production data.
type RestoreMetadata struct {
	corecontracts.ObjectMetadata
	SchemaVersion    string              `json:"schema_version"`
	RecoveryPointID  string              `json:"recovery_point_id"`
	BackupIDs        []string            `json:"backup_ids"`
	Mode             RestoreMode         `json:"mode"`
	State            RestoreState        `json:"state"`
	Target           ObjectReference     `json:"target"`
	RequestedObjects []ObjectReference   `json:"requested_objects"`
	Provider         ProviderMetadata    `json:"provider"`
	Fencing          FencingMetadata     `json:"fencing"`
	RequestedAt      time.Time           `json:"requested_at"`
	StartedAt        *time.Time          `json:"started_at,omitempty"`
	CompletedAt      *time.Time          `json:"completed_at,omitempty"`
	PITRTargetAt     *time.Time          `json:"pitr_target_at,omitempty"`
	Verification     RestoreVerification `json:"verification"`
	Failure          *FailureMetadata    `json:"failure,omitempty"`
}

type ObjectiveResult string

const (
	ObjectivePassed ObjectiveResult = "PASSED"
	ObjectiveFailed ObjectiveResult = "FAILED"
)

// RecoveryObjectiveEvidence records observed recovery results. Target values
// are policy inputs; observed values must come from an actual restore/drill.
type RecoveryObjectiveEvidence struct {
	corecontracts.ObjectMetadata
	SchemaVersion      string              `json:"schema_version"`
	PolicyID           string              `json:"policy_id"`
	Target             ObjectReference     `json:"target"`
	RecoveryPointID    string              `json:"recovery_point_id"`
	RestoreID          string              `json:"restore_id"`
	TargetRPOSeconds   uint64              `json:"target_rpo_seconds"`
	TargetRTOSeconds   uint64              `json:"target_rto_seconds"`
	ObservedRPOSeconds *uint64             `json:"observed_rpo_seconds,omitempty"`
	ObservedRTOSeconds *uint64             `json:"observed_rto_seconds,omitempty"`
	MeasuredAt         time.Time           `json:"measured_at"`
	Result             ObjectiveResult     `json:"result"`
	Evidence           []EvidenceReference `json:"evidence"`
	FailureReason      string              `json:"failure_reason,omitempty"`
}
