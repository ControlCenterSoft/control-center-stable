BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        VARCHAR(63) NOT NULL,
    name        VARCHAR(255) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT organizations_slug_format
        CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$')
);

CREATE UNIQUE INDEX organizations_slug_unique_ci
    ON organizations (LOWER(slug));

CREATE TABLE resources (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind             VARCHAR(64) NOT NULL,
    name             VARCHAR(255) NOT NULL,
    status           VARCHAR(64) NOT NULL DEFAULT 'unknown',
    external_id      VARCHAR(255),
    labels           JSONB NOT NULL DEFAULT '{}'::JSONB,
    specification    JSONB NOT NULL DEFAULT '{}'::JSONB,
    observed_state   JSONB NOT NULL DEFAULT '{}'::JSONB,
    revision         BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at       TIMESTAMPTZ,
    CONSTRAINT resources_kind_format CHECK (kind ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT resources_revision_positive CHECK (revision >= 1),
    CONSTRAINT resources_labels_object CHECK (jsonb_typeof(labels) = 'object'),
    CONSTRAINT resources_specification_object CHECK (jsonb_typeof(specification) = 'object'),
    CONSTRAINT resources_observed_state_object CHECK (jsonb_typeof(observed_state) = 'object')
);

CREATE INDEX resources_organization_kind_idx
    ON resources (organization_id, kind)
    WHERE deleted_at IS NULL;

CREATE INDEX resources_labels_gin_idx
    ON resources USING GIN (labels);

CREATE UNIQUE INDEX resources_external_identity_unique
    ON resources (organization_id, kind, external_id)
    WHERE external_id IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE config_revisions (
    id               BIGSERIAL PRIMARY KEY,
    organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    resource_id      UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    revision         BIGINT NOT NULL,
    configuration    JSONB NOT NULL,
    checksum_sha256  CHAR(64) NOT NULL,
    created_by       VARCHAR(255) NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT config_revisions_revision_positive CHECK (revision >= 1),
    CONSTRAINT config_revisions_configuration_object CHECK (jsonb_typeof(configuration) = 'object'),
    CONSTRAINT config_revisions_checksum_format CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT config_revisions_resource_revision_unique UNIQUE (resource_id, revision)
);

CREATE INDEX config_revisions_organization_created_idx
    ON config_revisions (organization_id, created_at DESC);

COMMIT;
