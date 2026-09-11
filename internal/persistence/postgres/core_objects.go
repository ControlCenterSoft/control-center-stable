package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"control-center/internal/corecontracts"
)

const coreObjectMutationLock int64 = 0x43434f424a5631 // "CCOBJV1"

const coreObjectColumns = `object_id,object_type,scope_id,owner_scope,generation,
resource_version,document::text,created_at,updated_at`

// CoreObjectRepository persists the canonical distributed core contracts.
// Mutations are serialized because topology validation requires a coherent
// snapshot spanning several object types.
type CoreObjectRepository struct {
	db         *sql.DB
	now        func() time.Time
	newVersion func() (string, error)
}

func NewCoreObjectRepository(db *sql.DB) (*CoreObjectRepository, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &CoreObjectRepository{db: db, now: time.Now, newVersion: corecontracts.NewOpaqueResourceVersion}, nil
}

func (r *CoreObjectRepository) List(ctx context.Context, filter corecontracts.ObjectFilter) ([]corecontracts.StoredObject, error) {
	if filter.ObjectType != "" && !filter.ObjectType.Valid() {
		return nil, fmt.Errorf("%w: unsupported object_type %q", corecontracts.ErrInvalidObject, filter.ObjectType)
	}
	for _, identifier := range []string{filter.ScopeID, filter.OwnerScope} {
		if identifier != "" {
			if err := corecontracts.ValidateObjectIdentifier(identifier); err != nil {
				return nil, err
			}
		}
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+coreObjectColumns+` FROM cc_core_objects
WHERE ($1='' OR object_type=$1) AND ($2='' OR scope_id=$2) AND ($3='' OR owner_scope=$3)
ORDER BY object_type,object_id`, string(filter.ObjectType), filter.ScopeID, filter.OwnerScope)
	if err != nil {
		return nil, fmt.Errorf("list distributed core objects: %w", err)
	}
	defer rows.Close()
	objects, err := scanCoreObjects(rows)
	if err != nil {
		return nil, err
	}
	if filter == (corecontracts.ObjectFilter{}) {
		if err := corecontracts.ValidateStoredObjects(objects); err != nil {
			return nil, fmt.Errorf("validate distributed core snapshot: %w", err)
		}
	}
	return objects, nil
}

func (r *CoreObjectRepository) Get(ctx context.Context, objectID string) (corecontracts.StoredObject, error) {
	if err := corecontracts.ValidateObjectIdentifier(objectID); err != nil {
		return corecontracts.StoredObject{}, err
	}
	object, err := scanCoreObject(r.db.QueryRowContext(ctx, `SELECT `+coreObjectColumns+` FROM cc_core_objects WHERE object_id=$1`, objectID))
	if errors.Is(err, sql.ErrNoRows) {
		return corecontracts.StoredObject{}, corecontracts.ErrObjectNotFound
	}
	if err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("get distributed core object: %w", err)
	}
	return object, nil
}

func (r *CoreObjectRepository) Apply(ctx context.Context, request corecontracts.MutationRequest, idempotencyKey string) (corecontracts.StoredObject, error) {
	if err := corecontracts.ValidateIdempotencyKey(idempotencyKey); err != nil {
		return corecontracts.StoredObject{}, err
	}
	fingerprint, err := corecontracts.MutationFingerprint(request)
	if err != nil {
		return corecontracts.StoredObject{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("begin distributed core mutation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, coreObjectMutationLock); err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("lock distributed core mutation: %w", err)
	}

	replayed, found, err := loadCoreMutationReceipt(ctx, tx, idempotencyKey, fingerprint)
	if err != nil {
		return corecontracts.StoredObject{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return corecontracts.StoredObject{}, fmt.Errorf("commit distributed core replay: %w", err)
		}
		return replayed, nil
	}

	snapshot, err := readCoreObjectSnapshot(ctx, tx)
	if err != nil {
		return corecontracts.StoredObject{}, err
	}
	if err := corecontracts.ValidateStoredObjects(snapshot); err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("validate stored distributed core snapshot: %w", err)
	}
	var current *corecontracts.StoredObject
	for index := range snapshot {
		if snapshot[index].ObjectID == request.ObjectID {
			copyOfObject := snapshot[index]
			current = &copyOfObject
			break
		}
	}
	resourceVersion, err := r.newVersion()
	if err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("allocate distributed core resource version: %w", err)
	}
	next, err := corecontracts.PrepareMutation(current, request, snapshot, r.now().UTC(), resourceVersion)
	if err != nil {
		return corecontracts.StoredObject{}, err
	}
	if request.Operation == corecontracts.MutationCreate {
		_, err = tx.ExecContext(ctx, `INSERT INTO cc_core_objects
(object_id,object_type,scope_id,owner_scope,generation,resource_version,document,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)`, next.ObjectID, string(next.ObjectType), next.ScopeID, next.OwnerScope, next.Generation, next.ResourceVersion, string(next.Document), next.CreatedAt, next.UpdatedAt)
		if isUniqueViolation(err) {
			return corecontracts.StoredObject{}, corecontracts.ErrObjectAlreadyExists
		}
	} else {
		result, updateErr := tx.ExecContext(ctx, `UPDATE cc_core_objects SET
scope_id=$3,owner_scope=$4,generation=$5,resource_version=$6,document=$7::jsonb,updated_at=$8
WHERE object_id=$1 AND object_type=$2 AND resource_version=$9`, next.ObjectID, string(next.ObjectType), next.ScopeID, next.OwnerScope, next.Generation, next.ResourceVersion, string(next.Document), next.UpdatedAt, request.Precondition.ResourceVersion)
		if updateErr != nil {
			err = updateErr
		} else if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
			err = rowsErr
		} else if affected != 1 {
			return corecontracts.StoredObject{}, corecontracts.ErrPreconditionFailed
		}
	}
	if err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("persist distributed core object: %w", err)
	}
	encodedResult, err := json.Marshal(next)
	if err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("encode distributed core mutation receipt: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_core_object_mutations
(idempotency_key,request_fingerprint,object_id,resulting_resource_version,result,applied_at)
VALUES ($1,$2,$3,$4,$5::jsonb,$6)`, idempotencyKey, fingerprint, next.ObjectID, next.ResourceVersion, string(encodedResult), next.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return corecontracts.StoredObject{}, corecontracts.ErrIdempotencyConflict
		}
		return corecontracts.StoredObject{}, fmt.Errorf("persist distributed core mutation receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return corecontracts.StoredObject{}, fmt.Errorf("commit distributed core mutation: %w", err)
	}
	return next, nil
}

func loadCoreMutationReceipt(ctx context.Context, tx *sql.Tx, key, fingerprint string) (corecontracts.StoredObject, bool, error) {
	var storedFingerprint, objectID, resourceVersion, encodedResult string
	err := tx.QueryRowContext(ctx, `SELECT request_fingerprint,object_id,resulting_resource_version,result::text
FROM cc_core_object_mutations WHERE idempotency_key=$1`, key).Scan(&storedFingerprint, &objectID, &resourceVersion, &encodedResult)
	if errors.Is(err, sql.ErrNoRows) {
		return corecontracts.StoredObject{}, false, nil
	}
	if err != nil {
		return corecontracts.StoredObject{}, false, fmt.Errorf("load distributed core mutation receipt: %w", err)
	}
	if storedFingerprint != fingerprint {
		return corecontracts.StoredObject{}, false, corecontracts.ErrIdempotencyConflict
	}
	var result corecontracts.StoredObject
	if err := json.Unmarshal([]byte(encodedResult), &result); err != nil {
		return corecontracts.StoredObject{}, false, fmt.Errorf("decode distributed core mutation receipt: %w", err)
	}
	if err := corecontracts.ValidateStoredObjectEnvelope(result); err != nil {
		return corecontracts.StoredObject{}, false, fmt.Errorf("validate distributed core mutation receipt: %w", err)
	}
	if result.ObjectID != objectID || result.ResourceVersion != resourceVersion {
		return corecontracts.StoredObject{}, false, errors.New("distributed core mutation receipt metadata is inconsistent")
	}
	return result, true, nil
}

func readCoreObjectSnapshot(ctx context.Context, tx *sql.Tx) ([]corecontracts.StoredObject, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+coreObjectColumns+` FROM cc_core_objects ORDER BY object_type,object_id`)
	if err != nil {
		return nil, fmt.Errorf("read distributed core snapshot: %w", err)
	}
	defer rows.Close()
	objects, err := scanCoreObjects(rows)
	if err != nil {
		return nil, err
	}
	return objects, nil
}

type coreObjectScanner interface{ Scan(...any) error }

func scanCoreObject(row coreObjectScanner) (corecontracts.StoredObject, error) {
	var result corecontracts.StoredObject
	var objectType, document string
	if err := row.Scan(&result.ObjectID, &objectType, &result.ScopeID, &result.OwnerScope, &result.Generation, &result.ResourceVersion, &document, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return corecontracts.StoredObject{}, err
	}
	result.ObjectType = corecontracts.ObjectType(objectType)
	result.Document = json.RawMessage(document)
	result.CreatedAt = result.CreatedAt.UTC()
	result.UpdatedAt = result.UpdatedAt.UTC()
	if err := corecontracts.ValidateStoredObjectEnvelope(result); err != nil {
		return corecontracts.StoredObject{}, err
	}
	return result, nil
}

type coreObjectRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanCoreObjects(rows coreObjectRows) ([]corecontracts.StoredObject, error) {
	objects := make([]corecontracts.StoredObject, 0)
	for rows.Next() {
		object, err := scanCoreObject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan distributed core object: %w", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distributed core objects: %w", err)
	}
	return objects, nil
}
