package corecontracts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
)

var (
	ErrObjectNotFound      = errors.New("distributed core object not found")
	ErrObjectAlreadyExists = errors.New("distributed core object already exists")
	ErrInvalidObject       = errors.New("invalid distributed core object")
	ErrInvalidMutation     = errors.New("invalid distributed core mutation")
	ErrIdempotencyConflict = errors.New("distributed core mutation idempotency conflict")
)

// ObjectType is the closed set persisted by the distributed core store.
type ObjectType string

const (
	ObjectScope            ObjectType = "scope"
	ObjectSite             ObjectType = "site"
	ObjectManagementZone   ObjectType = "management-zone"
	ObjectNetworkZone      ObjectType = "network-zone"
	ObjectNetworkInterface ObjectType = "network-interface"
	ObjectRoleAssignment   ObjectType = "role-assignment"
	ObjectDesiredState     ObjectType = "desired-state"
	ObjectActualState      ObjectType = "actual-state"
)

// Valid reports whether the object type has a canonical document schema.
func (t ObjectType) Valid() bool {
	switch t {
	case ObjectScope, ObjectSite, ObjectManagementZone, ObjectNetworkZone, ObjectNetworkInterface, ObjectRoleAssignment, ObjectDesiredState, ObjectActualState:
		return true
	default:
		return false
	}
}

// StoredObject separates synchronization metadata from a type-specific JSON
// document. Document is always a non-null JSON object and never contains the
// metadata fields duplicated in the envelope.
type StoredObject struct {
	ObjectMetadata
	ObjectType ObjectType      `json:"object_type"`
	Document   json.RawMessage `json:"document"`
}

// ObjectFilter restricts deterministic repository listings.
type ObjectFilter struct {
	ObjectType ObjectType
	ScopeID    string
	OwnerScope string
}

// MutationOperation is intentionally limited to create and CAS replacement.
// Deletion will be introduced only with object recovery/Recycle Bin semantics.
type MutationOperation string

const (
	MutationCreate  MutationOperation = "create"
	MutationReplace MutationOperation = "replace"
)

// MutationRequest is the typed input accepted by the Change/Job action. The
// persistence layer allocates timestamps, generation and resource_version.
type MutationRequest struct {
	Operation    MutationOperation   `json:"operation"`
	ObjectType   ObjectType          `json:"object_type"`
	ObjectID     string              `json:"object_id"`
	ScopeID      string              `json:"scope_id"`
	OwnerScope   string              `json:"owner_scope"`
	Document     json.RawMessage     `json:"document"`
	Precondition *ObjectPrecondition `json:"precondition,omitempty"`
}

// ObjectRepository is the durable/read model boundary shared by HTTP and the
// typed orchestration action.
type ObjectRepository interface {
	List(context.Context, ObjectFilter) ([]StoredObject, error)
	Get(context.Context, string) (StoredObject, error)
	Apply(context.Context, MutationRequest, string) (StoredObject, error)
}

// MutationFingerprint returns a stable digest for idempotency replay checks.
// JSON object key order and insignificant whitespace do not affect the digest.
func MutationFingerprint(request MutationRequest) (string, error) {
	var document any
	if err := decodeJSONValue(request.Document, &document); err != nil {
		return "", fmt.Errorf("%w: document: %v", ErrInvalidMutation, err)
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("%w: canonicalize document: %v", ErrInvalidMutation, err)
	}
	request.Document = canonical
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("%w: encode mutation fingerprint: %v", ErrInvalidMutation, err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// RoleAssignmentDocument is the metadata-free RoleAssignment payload.
type RoleAssignmentDocument struct {
	TargetNodeID      string   `json:"target_node_id"`
	ServiceIdentityID string   `json:"service_identity_id"`
	Role              NodeRole `json:"role"`
	SiteID            string   `json:"site_id,omitempty"`
	ManagementZoneID  string   `json:"management_zone_id,omitempty"`
}

// DesiredStateDocument is the metadata-free DesiredState payload.
type DesiredStateDocument struct {
	Kind           string          `json:"kind"`
	TargetObjectID string          `json:"target_object_id"`
	Spec           json.RawMessage `json:"spec"`
}

// ActualStateDocument is the metadata-free ActualState payload.
type ActualStateDocument struct {
	Kind               string            `json:"kind"`
	TargetObjectID     string            `json:"target_object_id"`
	DesiredObjectID    string            `json:"desired_object_id"`
	ObservedGeneration uint64            `json:"observed_generation"`
	SourceNodeID       string            `json:"source_node_id"`
	ObservedAt         time.Time         `json:"observed_at"`
	Status             ActualStateStatus `json:"status"`
	State              json.RawMessage   `json:"state"`
}

// LegacyGlobalScopeObject is the deterministic topology root introduced when
// a supported 0.3 database is upgraded to the distributed object schema.
func LegacyGlobalScopeObject(createdAt time.Time) StoredObject {
	createdAt = createdAt.UTC()
	return StoredObject{
		ObjectMetadata: ObjectMetadata{
			ObjectID: "global", ScopeID: "global", OwnerScope: "global",
			Generation: 1, ResourceVersion: "bootstrap-v0.3-global",
			CreatedAt: createdAt, UpdatedAt: createdAt,
		},
		ObjectType: ObjectScope,
		Document:   json.RawMessage(`{"id":"global","kind":"global","name":"Global"}`),
	}
}

// ValidateStoredObjects validates a complete snapshot, including topology and
// every typed document. It returns no partially valid result.
func ValidateStoredObjects(objects []StoredObject) error {
	if len(objects) == 0 {
		return fmt.Errorf("%w: at least the global scope is required", ErrInvalidObject)
	}
	seen := make(map[string]struct{}, len(objects))
	scopes := make([]Scope, 0)
	sites := make([]Site, 0)
	zones := make([]ManagementZone, 0)
	networkZones := make([]NetworkZone, 0)
	networkInterfaces := make([]NetworkInterface, 0)
	assignments := make([]RoleAssignment, 0)
	for _, object := range objects {
		if err := validateStoredEnvelope(object); err != nil {
			return err
		}
		if _, exists := seen[object.ObjectID]; exists {
			return fmt.Errorf("%w: duplicate object_id %q", ErrInvalidObject, object.ObjectID)
		}
		seen[object.ObjectID] = struct{}{}
		switch object.ObjectType {
		case ObjectScope:
			var scope Scope
			if err := decodeDocument(object.Document, &scope); err != nil {
				return invalidDocument(object, err)
			}
			scopes = append(scopes, scope)
		case ObjectSite:
			var site Site
			if err := decodeDocument(object.Document, &site); err != nil {
				return invalidDocument(object, err)
			}
			sites = append(sites, site)
		case ObjectManagementZone:
			var zone ManagementZone
			if err := decodeDocument(object.Document, &zone); err != nil {
				return invalidDocument(object, err)
			}
			zones = append(zones, zone)
		case ObjectNetworkZone:
			var zone NetworkZone
			if err := decodeDocument(object.Document, &zone); err != nil {
				return invalidDocument(object, err)
			}
			networkZones = append(networkZones, zone)
		case ObjectNetworkInterface:
			var networkInterface NetworkInterface
			if err := decodeDocument(object.Document, &networkInterface); err != nil {
				return invalidDocument(object, err)
			}
			networkInterfaces = append(networkInterfaces, networkInterface)
		case ObjectRoleAssignment:
			var document RoleAssignmentDocument
			if err := decodeDocument(object.Document, &document); err != nil {
				return invalidDocument(object, err)
			}
			assignments = append(assignments, roleAssignmentFrom(object.ObjectMetadata, document))
		}
	}

	topology, err := NewTopology(scopes, sites, zones)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidObject, err)
	}
	for _, object := range objects {
		if err := validateTypedObject(object, topology); err != nil {
			return err
		}
	}
	if err := ValidateRoleAssignments(assignments, topology); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidObject, err)
	}
	if err := ValidateNetworkContracts(networkZones, networkInterfaces, assignments, topology); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidObject, err)
	}
	return nil
}

// PrepareMutation builds and validates a server-versioned object without
// persisting it. Repositories must still perform the precondition atomically
// with their write.
func PrepareMutation(current *StoredObject, request MutationRequest, snapshot []StoredObject, now time.Time, resourceVersion string) (StoredObject, error) {
	if now.IsZero() {
		return StoredObject{}, fmt.Errorf("%w: mutation time is required", ErrInvalidMutation)
	}
	if request.Operation != MutationCreate && request.Operation != MutationReplace {
		return StoredObject{}, fmt.Errorf("%w: unsupported operation %q", ErrInvalidMutation, request.Operation)
	}
	if !request.ObjectType.Valid() {
		return StoredObject{}, fmt.Errorf("%w: unsupported object_type %q", ErrInvalidMutation, request.ObjectType)
	}
	if err := validateJSONObject("document", request.Document); err != nil {
		return StoredObject{}, fmt.Errorf("%w: %v", ErrInvalidMutation, err)
	}

	now = now.UTC()
	next := StoredObject{
		ObjectMetadata: ObjectMetadata{
			ObjectID: request.ObjectID, ScopeID: request.ScopeID, OwnerScope: request.OwnerScope,
			Generation: 1, ResourceVersion: resourceVersion, CreatedAt: now, UpdatedAt: now,
		},
		ObjectType: request.ObjectType,
		Document:   canonicalDocument(request.Document),
	}
	if request.Operation == MutationCreate {
		if current != nil {
			return StoredObject{}, ErrObjectAlreadyExists
		}
		for _, existing := range snapshot {
			if existing.ObjectID == request.ObjectID {
				return StoredObject{}, ErrObjectAlreadyExists
			}
		}
		if request.Precondition != nil {
			return StoredObject{}, fmt.Errorf("%w: create does not accept an object precondition", ErrInvalidMutation)
		}
	} else {
		if current == nil {
			return StoredObject{}, ErrObjectNotFound
		}
		if request.Precondition == nil {
			return StoredObject{}, ErrPreconditionRequired
		}
		if err := request.Precondition.ValidateAgainst(current.ObjectMetadata); err != nil {
			return StoredObject{}, err
		}
		if request.ObjectType != current.ObjectType {
			return StoredObject{}, fmt.Errorf("%w: object_type is immutable", ErrInvalidTransition)
		}
		next.CreatedAt = current.CreatedAt
		desiredChanged := request.ScopeID != current.ScopeID ||
			request.OwnerScope != current.OwnerScope ||
			!jsonDocumentsEqual(request.Document, current.Document)
		if request.ObjectType == ObjectActualState {
			desiredChanged = false
		}
		next.Generation = current.Generation
		if desiredChanged {
			next.Generation++
		}
		if err := validateTypedSuccessor(*current, next, snapshot); err != nil {
			return StoredObject{}, err
		}
		if err := ValidateSuccessor(current.ObjectMetadata, next.ObjectMetadata, desiredChanged); err != nil {
			return StoredObject{}, err
		}
	}

	candidateSnapshot := replaceInSnapshot(snapshot, next)
	if err := ValidateStoredObjects(candidateSnapshot); err != nil {
		return StoredObject{}, err
	}
	return cloneStoredObject(next), nil
}

// NewOpaqueResourceVersion returns an unpredictable token. Its content has no
// ordering semantics.
func NewOpaqueResourceVersion() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate resource version: %w", err)
	}
	return "rv-" + hex.EncodeToString(value[:]), nil
}

// ValidateIdempotencyKey checks the durable worker-to-store replay key.
func ValidateIdempotencyKey(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: idempotency key is required and must not have surrounding whitespace", ErrInvalidMutation)
	}
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("%w: idempotency key exceeds %d bytes", ErrInvalidMutation, maxIdentifierLength)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%w: idempotency key contains whitespace or control characters", ErrInvalidMutation)
		}
	}
	return nil
}

// ValidateStoredObjectEnvelope checks storage-level invariants that do not
// require the rest of the topology snapshot.
func ValidateStoredObjectEnvelope(object StoredObject) error {
	return validateStoredEnvelope(object)
}

// ValidateObjectIdentifier checks the common object/scope identifier syntax.
func ValidateObjectIdentifier(value string) error {
	if err := validateIdentifier("identifier", value); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidObject, err)
	}
	return nil
}

func validateStoredEnvelope(object StoredObject) error {
	if err := object.ObjectMetadata.Validate(); err != nil {
		return fmt.Errorf("%w: object %q: %v", ErrInvalidObject, object.ObjectID, err)
	}
	if !object.ObjectType.Valid() {
		return fmt.Errorf("%w: object %q has unsupported type %q", ErrInvalidObject, object.ObjectID, object.ObjectType)
	}
	if err := validateJSONObject("document", object.Document); err != nil {
		return fmt.Errorf("%w: object %q: %v", ErrInvalidObject, object.ObjectID, err)
	}
	return nil
}

func validateTypedObject(object StoredObject, topology Topology) error {
	switch object.ObjectType {
	case ObjectScope:
		var scope Scope
		if err := decodeDocument(object.Document, &scope); err != nil {
			return invalidDocument(object, err)
		}
		if scope.ID != object.ObjectID {
			return fmt.Errorf("%w: scope document id must equal object_id", ErrInvalidObject)
		}
		if scope.Kind == ScopeGlobal {
			if object.ScopeID != scope.ID || object.OwnerScope != scope.ID {
				return fmt.Errorf("%w: global scope metadata must be self-owned", ErrInvalidObject)
			}
			return nil
		}
		if object.ScopeID != scope.ParentID || !topology.Contains(object.OwnerScope, scope.ID) {
			return fmt.Errorf("%w: scope metadata does not match its hierarchy", ErrInvalidObject)
		}
		if object.OwnerScope != topology.RootID() && !topology.IsDelegated(object.OwnerScope, DelegateConfiguration) {
			return fmt.Errorf("%w: scope owner has no configuration delegation", ErrInvalidObject)
		}
	case ObjectSite:
		var site Site
		if err := decodeDocument(object.Document, &site); err != nil {
			return invalidDocument(object, err)
		}
		if site.ID != object.ObjectID || site.ScopeID != object.ScopeID || !topology.Contains(object.OwnerScope, object.ScopeID) {
			return fmt.Errorf("%w: site metadata does not match its document or hierarchy", ErrInvalidObject)
		}
		if object.OwnerScope != topology.RootID() && !topology.IsDelegated(object.OwnerScope, DelegateConfiguration) {
			return fmt.Errorf("%w: site owner has no configuration delegation", ErrInvalidObject)
		}
	case ObjectManagementZone:
		var zone ManagementZone
		if err := decodeDocument(object.Document, &zone); err != nil {
			return invalidDocument(object, err)
		}
		if zone.ID != object.ObjectID || zone.ScopeID != object.ScopeID || !topology.Contains(object.OwnerScope, object.ScopeID) {
			return fmt.Errorf("%w: management-zone metadata does not match its document or hierarchy", ErrInvalidObject)
		}
		if object.OwnerScope != topology.RootID() && !topology.IsDelegated(object.OwnerScope, DelegateConfiguration) {
			return fmt.Errorf("%w: management-zone owner has no configuration delegation", ErrInvalidObject)
		}
	case ObjectNetworkZone:
		var zone NetworkZone
		if err := decodeDocument(object.Document, &zone); err != nil {
			return invalidDocument(object, err)
		}
		if zone.ID != object.ObjectID || zone.ScopeID != object.ScopeID || !topology.Contains(object.OwnerScope, object.ScopeID) {
			return fmt.Errorf("%w: network-zone metadata does not match its document or hierarchy", ErrInvalidObject)
		}
		if object.OwnerScope != topology.RootID() && !topology.IsDelegated(object.OwnerScope, DelegateConfiguration) {
			return fmt.Errorf("%w: network-zone owner has no configuration delegation", ErrInvalidObject)
		}
	case ObjectNetworkInterface:
		var networkInterface NetworkInterface
		if err := decodeDocument(object.Document, &networkInterface); err != nil {
			return invalidDocument(object, err)
		}
		if networkInterface.ID != object.ObjectID || networkInterface.ScopeID != object.ScopeID || !topology.Contains(object.OwnerScope, object.ScopeID) {
			return fmt.Errorf("%w: network-interface metadata does not match its document or hierarchy", ErrInvalidObject)
		}
		if object.OwnerScope != topology.RootID() && !topology.IsDelegated(object.OwnerScope, DelegateConfiguration) {
			return fmt.Errorf("%w: network-interface owner has no configuration delegation", ErrInvalidObject)
		}
	case ObjectRoleAssignment:
		var document RoleAssignmentDocument
		if err := decodeDocument(object.Document, &document); err != nil {
			return invalidDocument(object, err)
		}
		if err := ValidateRoleAssignment(roleAssignmentFrom(object.ObjectMetadata, document), topology); err != nil {
			return fmt.Errorf("%w: object %q: %v", ErrInvalidObject, object.ObjectID, err)
		}
	case ObjectDesiredState:
		var document DesiredStateDocument
		if err := decodeDocument(object.Document, &document); err != nil {
			return invalidDocument(object, err)
		}
		if err := ValidateDesiredState(desiredStateFrom(object.ObjectMetadata, document), topology); err != nil {
			return fmt.Errorf("%w: object %q: %v", ErrInvalidObject, object.ObjectID, err)
		}
	case ObjectActualState:
		var document ActualStateDocument
		if err := decodeDocument(object.Document, &document); err != nil {
			return invalidDocument(object, err)
		}
		if err := ValidateActualState(actualStateFrom(object.ObjectMetadata, document), topology); err != nil {
			return fmt.Errorf("%w: object %q: %v", ErrInvalidObject, object.ObjectID, err)
		}
	}
	return nil
}

func validateTypedSuccessor(current, next StoredObject, snapshot []StoredObject) error {
	objects := replaceInSnapshot(snapshot, next)
	topology, err := topologyFromObjects(objects)
	if err != nil {
		return err
	}
	switch current.ObjectType {
	case ObjectScope:
		var before, after Scope
		if err := decodeDocument(current.Document, &before); err != nil {
			return invalidDocument(current, err)
		}
		if err := decodeDocument(next.Document, &after); err != nil {
			return invalidDocument(next, err)
		}
		if before.Kind != after.Kind {
			return fmt.Errorf("%w: scope kind is immutable", ErrInvalidTransition)
		}
	case ObjectRoleAssignment:
		var before, after RoleAssignmentDocument
		if err := decodeDocument(current.Document, &before); err != nil {
			return invalidDocument(current, err)
		}
		if err := decodeDocument(next.Document, &after); err != nil {
			return invalidDocument(next, err)
		}
		return ValidateRoleAssignmentSuccessor(roleAssignmentFrom(current.ObjectMetadata, before), roleAssignmentFrom(next.ObjectMetadata, after), topology)
	case ObjectNetworkZone:
		var before, after NetworkZone
		if err := decodeDocument(current.Document, &before); err != nil {
			return invalidDocument(current, err)
		}
		if err := decodeDocument(next.Document, &after); err != nil {
			return invalidDocument(next, err)
		}
		if before.Kind != after.Kind || before.SiteID != after.SiteID {
			return fmt.Errorf("%w: Network Zone kind and site are immutable", ErrInvalidTransition)
		}
	case ObjectNetworkInterface:
		var before, after NetworkInterface
		if err := decodeDocument(current.Document, &before); err != nil {
			return invalidDocument(current, err)
		}
		if err := decodeDocument(next.Document, &after); err != nil {
			return invalidDocument(next, err)
		}
		if before.NodeID != after.NodeID || before.Kind != after.Kind || before.SiteID != after.SiteID {
			return fmt.Errorf("%w: Network Interface node, kind, and site are immutable", ErrInvalidTransition)
		}
	case ObjectDesiredState:
		var before, after DesiredStateDocument
		if err := decodeDocument(current.Document, &before); err != nil {
			return invalidDocument(current, err)
		}
		if err := decodeDocument(next.Document, &after); err != nil {
			return invalidDocument(next, err)
		}
		if before.Kind != after.Kind || before.TargetObjectID != after.TargetObjectID {
			return fmt.Errorf("%w: Desired State kind and target are immutable", ErrInvalidTransition)
		}
	case ObjectActualState:
		var before, after ActualStateDocument
		if err := decodeDocument(current.Document, &before); err != nil {
			return invalidDocument(current, err)
		}
		if err := decodeDocument(next.Document, &after); err != nil {
			return invalidDocument(next, err)
		}
		if before.Kind != after.Kind || before.TargetObjectID != after.TargetObjectID ||
			before.DesiredObjectID != after.DesiredObjectID || before.SourceNodeID != after.SourceNodeID {
			return fmt.Errorf("%w: Actual State identity fields are immutable", ErrInvalidTransition)
		}
		if after.ObservedGeneration < before.ObservedGeneration {
			return fmt.Errorf("%w: Actual State observed_generation moved backwards", ErrInvalidTransition)
		}
		if after.ObservedAt.Before(before.ObservedAt) {
			return fmt.Errorf("%w: Actual State observed_at moved backwards", ErrInvalidTransition)
		}
	}
	return nil
}

func topologyFromObjects(objects []StoredObject) (Topology, error) {
	scopes := make([]Scope, 0)
	sites := make([]Site, 0)
	zones := make([]ManagementZone, 0)
	for _, object := range objects {
		switch object.ObjectType {
		case ObjectScope:
			var value Scope
			if err := decodeDocument(object.Document, &value); err != nil {
				return Topology{}, invalidDocument(object, err)
			}
			scopes = append(scopes, value)
		case ObjectSite:
			var value Site
			if err := decodeDocument(object.Document, &value); err != nil {
				return Topology{}, invalidDocument(object, err)
			}
			sites = append(sites, value)
		case ObjectManagementZone:
			var value ManagementZone
			if err := decodeDocument(object.Document, &value); err != nil {
				return Topology{}, invalidDocument(object, err)
			}
			zones = append(zones, value)
		}
	}
	topology, err := NewTopology(scopes, sites, zones)
	if err != nil {
		return Topology{}, fmt.Errorf("%w: %v", ErrInvalidObject, err)
	}
	return topology, nil
}

func roleAssignmentFrom(metadata ObjectMetadata, document RoleAssignmentDocument) RoleAssignment {
	return RoleAssignment{ObjectMetadata: metadata, TargetNodeID: document.TargetNodeID, ServiceIdentityID: document.ServiceIdentityID, Role: document.Role, SiteID: document.SiteID, ManagementZoneID: document.ManagementZoneID}
}

func desiredStateFrom(metadata ObjectMetadata, document DesiredStateDocument) DesiredState {
	return DesiredState{ObjectMetadata: metadata, Kind: document.Kind, TargetObjectID: document.TargetObjectID, Spec: append(json.RawMessage(nil), document.Spec...)}
}

func actualStateFrom(metadata ObjectMetadata, document ActualStateDocument) ActualState {
	return ActualState{ObjectMetadata: metadata, Kind: document.Kind, TargetObjectID: document.TargetObjectID, DesiredObjectID: document.DesiredObjectID, ObservedGeneration: document.ObservedGeneration, SourceNodeID: document.SourceNodeID, ObservedAt: document.ObservedAt, Status: document.Status, State: append(json.RawMessage(nil), document.State...)}
}

func replaceInSnapshot(snapshot []StoredObject, next StoredObject) []StoredObject {
	result := make([]StoredObject, 0, len(snapshot)+1)
	replaced := false
	for _, object := range snapshot {
		if object.ObjectID == next.ObjectID {
			if !replaced {
				result = append(result, cloneStoredObject(next))
				replaced = true
			}
			continue
		}
		result = append(result, cloneStoredObject(object))
	}
	if !replaced {
		result = append(result, cloneStoredObject(next))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ObjectID < result[j].ObjectID })
	return result
}

func cloneStoredObject(object StoredObject) StoredObject {
	object.Document = append(json.RawMessage(nil), object.Document...)
	return object
}

func canonicalDocument(document json.RawMessage) json.RawMessage {
	var compact bytes.Buffer
	if err := json.Compact(&compact, document); err != nil {
		return append(json.RawMessage(nil), document...)
	}
	return append(json.RawMessage(nil), compact.Bytes()...)
}

func jsonDocumentsEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if decodeJSONValue(left, &leftValue) != nil || decodeJSONValue(right, &rightValue) != nil {
		return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func decodeJSONValue(document json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("document has trailing JSON")
	}
	return nil
}

func decodeDocument(document json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("document has trailing JSON")
	}
	return nil
}

func invalidDocument(object StoredObject, err error) error {
	return fmt.Errorf("%w: %s object %q document: %v", ErrInvalidObject, object.ObjectType, object.ObjectID, err)
}
