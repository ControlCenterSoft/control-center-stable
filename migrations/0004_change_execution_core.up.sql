BEGIN;

CREATE TABLE IF NOT EXISTS cc_config_revisions (
    id              text PRIMARY KEY,
    sequence        bigint NOT NULL UNIQUE CHECK (sequence > 0),
    digest          text NOT NULL UNIQUE CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    content         json NOT NULL,
    created_at      timestamptz NOT NULL,
    created_by      text NOT NULL
);

CREATE TABLE IF NOT EXISTS cc_idempotency_keys (
    scope           text NOT NULL,
    key             text NOT NULL,
    fingerprint     text NOT NULL,
    resource_id     text NOT NULL,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (scope, key)
);

CREATE OR REPLACE FUNCTION cc_reject_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'configuration revisions are immutable';
END;
$$;

DROP TRIGGER IF EXISTS cc_config_revisions_immutable ON cc_config_revisions;
CREATE TRIGGER cc_config_revisions_immutable
BEFORE UPDATE OR DELETE ON cc_config_revisions
FOR EACH ROW EXECUTE FUNCTION cc_reject_revision_mutation();

CREATE TABLE IF NOT EXISTS cc_policy_decisions (
    id                      text PRIMARY KEY,
    policy_id               text NOT NULL,
    effect                  text NOT NULL CHECK (effect IN ('allow', 'deny')),
    risk                    text NOT NULL CHECK (risk IN ('low', 'medium', 'high', 'critical')),
    reason                  text NOT NULL,
    minimum_approvals       integer NOT NULL DEFAULT 0 CHECK (minimum_approvals >= 0),
    approval_permission     text,
    distinct_actors         boolean NOT NULL DEFAULT true,
    prohibit_requester      boolean NOT NULL DEFAULT false,
    evaluated_at            timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS cc_changes (
    id                  text PRIMARY KEY,
    action_name         text NOT NULL,
    requester           text NOT NULL,
    revision_id         text NOT NULL REFERENCES cc_config_revisions(id),
    decision_id         text NOT NULL REFERENCES cc_policy_decisions(id),
    risk                text NOT NULL CHECK (risk IN ('low', 'medium', 'high', 'critical')),
    input               jsonb NOT NULL,
    idempotency_key     text NOT NULL UNIQUE,
    input_fingerprint   text NOT NULL,
    job_id              text UNIQUE,
    state               text NOT NULL CHECK (state IN (
                            'pending_approval', 'approved', 'queued', 'executing',
                            'verifying', 'succeeded', 'failed', 'cancelled', 'rejected'
                        )),
    version             bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS cc_change_approvals (
    change_id           text NOT NULL REFERENCES cc_changes(id) ON DELETE CASCADE,
    actor               text NOT NULL,
    permissions         text[] NOT NULL DEFAULT '{}',
    approved_at         timestamptz NOT NULL,
    PRIMARY KEY (change_id, actor)
);

CREATE TABLE IF NOT EXISTS cc_jobs (
    id                  text PRIMARY KEY,
    change_id           text NOT NULL REFERENCES cc_changes(id),
    action_name         text NOT NULL,
    input               jsonb NOT NULL,
    idempotency_key     text NOT NULL UNIQUE,
    input_fingerprint   text NOT NULL,
    status              text NOT NULL CHECK (status IN (
                            'queued', 'running', 'retry_wait', 'cancel_requested',
                            'cancelled', 'succeeded', 'failed'
                        )),
    attempt             integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts        integer NOT NULL CHECK (max_attempts > 0),
    next_attempt_at     timestamptz,
    lease_token         text,
    lease_worker_id     text,
    lease_expires_at    timestamptz,
    output              jsonb,
    last_error          text,
    version             bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    CHECK ((lease_token IS NULL) = (lease_worker_id IS NULL)),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX IF NOT EXISTS cc_jobs_claim_idx
    ON cc_jobs (status, next_attempt_at, created_at)
    WHERE status IN ('queued', 'retry_wait', 'running');
CREATE INDEX IF NOT EXISTS cc_jobs_change_idx ON cc_jobs (change_id, created_at);

CREATE TABLE IF NOT EXISTS cc_actual_states (
    job_id              text NOT NULL REFERENCES cc_jobs(id) ON DELETE CASCADE,
    resource_id         text NOT NULL,
    kind                text NOT NULL,
    state               text NOT NULL CHECK (state IN ('present', 'absent', 'unknown')),
    observed_at         timestamptz NOT NULL,
    revision_id         text,
    details             jsonb,
    PRIMARY KEY (job_id, resource_id)
);

CREATE TABLE IF NOT EXISTS cc_health_observations (
    job_id              text NOT NULL REFERENCES cc_jobs(id) ON DELETE CASCADE,
    resource_id         text NOT NULL,
    status              text NOT NULL CHECK (status IN ('healthy', 'degraded', 'failed', 'unknown')),
    checked_at          timestamptz NOT NULL,
    message             text,
    PRIMARY KEY (job_id, resource_id)
);

-- cc_audit_events is owned by migration 0002. Reuse that append-only schema
-- instead of attempting to redefine it with incompatible column types.
CREATE INDEX IF NOT EXISTS cc_audit_events_correlation_idx
    ON cc_audit_events (correlation_id, occurred_at DESC);

INSERT INTO cc_rbac_permissions (name, description) VALUES
    ('config.revisions.write', 'Create immutable configuration revisions'),
    ('orchestration.actions.read', 'Read executable action descriptors'),
    ('orchestration.changes.write', 'Create changes'),
    ('orchestration.changes.approve', 'Approve high-risk changes'),
    ('orchestration.jobs.read', 'Read job execution state and outputs'),
    ('orchestration.jobs.cancel', 'Request job cancellation'),
    ('orchestration.actions.execute', 'Execute allowlisted orchestration actions')
ON CONFLICT (name) DO NOTHING;

INSERT INTO cc_rbac_role_permissions (role_name, permission_name) VALUES
    ('operator', 'config.revisions.write'),
    ('operator', 'orchestration.actions.read'),
    ('operator', 'orchestration.changes.write'),
    ('operator', 'orchestration.jobs.read'),
    ('operator', 'orchestration.jobs.cancel'),
    ('auditor', 'orchestration.actions.read'),
    ('auditor', 'orchestration.jobs.read'),
    ('viewer', 'orchestration.actions.read')
ON CONFLICT DO NOTHING;

COMMIT;
