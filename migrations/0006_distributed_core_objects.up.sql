BEGIN;

CREATE TABLE IF NOT EXISTS cc_core_objects (
    object_id varchar(255) PRIMARY KEY,
    object_type varchar(32) NOT NULL,
    scope_id varchar(255) NOT NULL,
    owner_scope varchar(255) NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    resource_version varchar(255) NOT NULL UNIQUE,
    document jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT cc_core_objects_type CHECK (object_type IN (
        'scope', 'site', 'management-zone', 'role-assignment', 'desired-state', 'actual-state'
    )),
    CONSTRAINT cc_core_objects_object_id_format CHECK (
        object_id ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_core_objects_scope_id_format CHECK (
        scope_id ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_core_objects_owner_scope_format CHECK (
        owner_scope ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_core_objects_resource_version_format CHECK (
        length(resource_version) BETWEEN 1 AND 255 AND resource_version !~ '[[:space:]]'
    ),
    CONSTRAINT cc_core_objects_document_object CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT cc_core_objects_timestamp_order CHECK (updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS cc_core_objects_type_id_idx
    ON cc_core_objects (object_type, object_id);
CREATE INDEX IF NOT EXISTS cc_core_objects_scope_type_idx
    ON cc_core_objects (scope_id, object_type, object_id);
CREATE INDEX IF NOT EXISTS cc_core_objects_owner_type_idx
    ON cc_core_objects (owner_scope, object_type, object_id);

-- Receipts make worker retries idempotent even after later object updates.
-- A key is permanently bound to one semantic request fingerprint.
CREATE TABLE IF NOT EXISTS cc_core_object_mutations (
    idempotency_key varchar(255) PRIMARY KEY,
    request_fingerprint char(64) NOT NULL,
    object_id varchar(255) NOT NULL REFERENCES cc_core_objects(object_id),
    resulting_resource_version varchar(255) NOT NULL,
    result jsonb NOT NULL,
    applied_at timestamptz NOT NULL,
    CONSTRAINT cc_core_object_mutations_key_format CHECK (
        length(idempotency_key) BETWEEN 1 AND 255 AND idempotency_key !~ '[[:space:]]'
    ),
    CONSTRAINT cc_core_object_mutations_fingerprint_format CHECK (
        request_fingerprint ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT cc_core_object_mutations_resource_version_format CHECK (
        length(resulting_resource_version) BETWEEN 1 AND 255
        AND resulting_resource_version !~ '[[:space:]]'
    ),
    CONSTRAINT cc_core_object_mutations_result_object CHECK (jsonb_typeof(result) = 'object')
);

CREATE INDEX IF NOT EXISTS cc_core_object_mutations_object_idx
    ON cc_core_object_mutations (object_id, applied_at);

-- A 0.3 installation has no distributed topology records. The one canonical
-- root is inserted deterministically while all 0.3 tables and data stay intact.
INSERT INTO cc_core_objects (
    object_id, object_type, scope_id, owner_scope, generation,
    resource_version, document, created_at, updated_at
) VALUES (
    'global', 'scope', 'global', 'global', 1,
    'bootstrap-v0.3-global',
    '{"id":"global","kind":"global","name":"Global"}'::jsonb,
    transaction_timestamp(), transaction_timestamp()
) ON CONFLICT (object_id) DO NOTHING;

INSERT INTO cc_rbac_permissions (name, description) VALUES
    ('core.objects.read', 'Read distributed core topology and state objects'),
    ('core.objects.write', 'Apply typed distributed core object changes through Change and Job')
ON CONFLICT (name) DO NOTHING;

INSERT INTO cc_rbac_role_permissions (role_name, permission_name) VALUES
    ('operator', 'core.objects.read'),
    ('operator', 'core.objects.write'),
    ('auditor', 'core.objects.read'),
    ('viewer', 'core.objects.read')
ON CONFLICT DO NOTHING;

COMMIT;
