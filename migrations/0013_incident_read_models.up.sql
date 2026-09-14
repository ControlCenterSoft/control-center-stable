BEGIN;

CREATE TABLE IF NOT EXISTS cc_incident_read_models (
    object_id         varchar(255) PRIMARY KEY,
    scope_id          varchar(255) NOT NULL,
    owner_scope       varchar(255) NOT NULL,
    generation        bigint       NOT NULL CHECK (generation > 0),
    resource_version  varchar(255) NOT NULL UNIQUE,
    severity          text         NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status            text         NOT NULL CHECK (status IN ('open', 'acknowledged', 'resolved')),
    title             varchar(160) NOT NULL,
    started_at        timestamptz  NOT NULL,
    last_observed_at  timestamptz  NOT NULL,
    document          jsonb        NOT NULL,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL,
    CONSTRAINT cc_incident_read_models_object_id_format CHECK (
        object_id ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_incident_read_models_scope_id_format CHECK (
        scope_id ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_incident_read_models_owner_scope_format CHECK (
        owner_scope ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_incident_read_models_resource_version_format CHECK (
        length(resource_version) BETWEEN 1 AND 255 AND resource_version !~ '[[:space:]]'
    ),
    CONSTRAINT cc_incident_read_models_title_nonempty CHECK (length(title) > 0),
    CONSTRAINT cc_incident_read_models_document_object CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT cc_incident_read_models_observation_order CHECK (last_observed_at >= started_at),
    CONSTRAINT cc_incident_read_models_lifetime_order CHECK (
        started_at >= created_at AND last_observed_at <= updated_at AND updated_at >= created_at
    )
);

CREATE INDEX IF NOT EXISTS cc_incident_read_models_scope_order_idx
    ON cc_incident_read_models (scope_id, started_at DESC, object_id ASC);

CREATE INDEX IF NOT EXISTS cc_incident_read_models_status_order_idx
    ON cc_incident_read_models (scope_id, status, started_at DESC, object_id ASC);

CREATE INDEX IF NOT EXISTS cc_incident_read_models_severity_order_idx
    ON cc_incident_read_models (scope_id, severity, started_at DESC, object_id ASC);

CREATE TABLE IF NOT EXISTS cc_incident_affected_resources (
    incident_id   varchar(255) NOT NULL REFERENCES cc_incident_read_models(object_id) ON DELETE CASCADE,
    resource_kind varchar(255) NOT NULL,
    resource_id   varchar(255) NOT NULL,
    scope_id      varchar(255) NOT NULL,
    PRIMARY KEY (incident_id, resource_kind, resource_id, scope_id),
    CONSTRAINT cc_incident_resources_kind_format CHECK (
        resource_kind ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_incident_resources_id_format CHECK (
        resource_id ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,253}[A-Za-z0-9])?$'
    ),
    CONSTRAINT cc_incident_resources_scope_format CHECK (
        scope_id ~ '^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,253}[A-Za-z0-9])?$'
    )
);

CREATE INDEX IF NOT EXISTS cc_incident_affected_resources_lookup_idx
    ON cc_incident_affected_resources (resource_kind, resource_id, incident_id);

COMMIT;
