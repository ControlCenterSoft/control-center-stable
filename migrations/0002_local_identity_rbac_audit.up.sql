CREATE TABLE cc_local_users (
    id                 uuid PRIMARY KEY,
    username           text NOT NULL,
    display_name       text NOT NULL DEFAULT '',
    password_hash      text NOT NULL CHECK (password_hash LIKE '$argon2id$v=19$%'),
    enabled            boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_login_at      timestamptz,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT cc_local_users_username_length CHECK (length(username) BETWEEN 1 AND 128)
);

CREATE UNIQUE INDEX cc_local_users_username_ci_uq ON cc_local_users (lower(username));

CREATE TABLE cc_auth_sessions (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES cc_local_users(id) ON DELETE CASCADE,
    token_digest char(64) NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    source_ip    inet,
    user_agent   varchar(512),
    CONSTRAINT cc_auth_sessions_expiry CHECK (expires_at > created_at),
    CONSTRAINT cc_auth_sessions_revoke_time CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX cc_auth_sessions_active_user_idx
    ON cc_auth_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE cc_rbac_roles (
    name        varchar(128) PRIMARY KEY,
    description text NOT NULL DEFAULT '',
    built_in    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE cc_rbac_permissions (
    name        varchar(192) PRIMARY KEY,
    description text NOT NULL DEFAULT ''
);

CREATE TABLE cc_rbac_role_permissions (
    role_name       varchar(128) NOT NULL REFERENCES cc_rbac_roles(name) ON DELETE CASCADE,
    permission_name varchar(192) NOT NULL REFERENCES cc_rbac_permissions(name) ON DELETE RESTRICT,
    PRIMARY KEY (role_name, permission_name)
);

CREATE TABLE cc_rbac_user_bindings (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES cc_local_users(id) ON DELETE CASCADE,
    role_name  varchar(128) NOT NULL REFERENCES cc_rbac_roles(name) ON DELETE CASCADE,
    scope_kind varchar(16) NOT NULL CHECK (scope_kind IN ('global', 'tenant', 'site', 'resource')),
    scope_id   text,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid REFERENCES cc_local_users(id) ON DELETE SET NULL,
    CONSTRAINT cc_rbac_binding_scope CHECK (
        (scope_kind = 'global' AND scope_id IS NULL) OR
        (scope_kind <> 'global' AND scope_id IS NOT NULL AND length(scope_id) > 0)
    ),
    UNIQUE (user_id, role_name, scope_kind, scope_id)
);

CREATE INDEX cc_rbac_user_bindings_user_idx ON cc_rbac_user_bindings (user_id);

CREATE TABLE cc_audit_events (
    sequence_id    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id             uuid NOT NULL UNIQUE,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    action         varchar(192) NOT NULL,
    outcome        varchar(32) NOT NULL,
    actor_id       uuid REFERENCES cc_local_users(id) ON DELETE SET NULL,
    subject_id     text,
    source_ip      inet,
    correlation_id text,
    details        jsonb NOT NULL DEFAULT '{}'::jsonb,
    previous_hash  char(64),
    hash           char(64) NOT NULL UNIQUE
);

CREATE INDEX cc_audit_events_time_idx ON cc_audit_events (occurred_at DESC);
CREATE INDEX cc_audit_events_actor_idx ON cc_audit_events (actor_id, occurred_at DESC);

CREATE OR REPLACE FUNCTION cc_deny_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    RAISE EXCEPTION 'cc_audit_events is append-only';
END;
$function$;

CREATE TRIGGER cc_audit_events_no_update
BEFORE UPDATE ON cc_audit_events
FOR EACH ROW EXECUTE FUNCTION cc_deny_audit_mutation();

CREATE TRIGGER cc_audit_events_no_delete
BEFORE DELETE ON cc_audit_events
FOR EACH ROW EXECUTE FUNCTION cc_deny_audit_mutation();

-- Roles and permissions are definitions, not grants. No user receives a role
-- during migration; the installer must create the first local administrator
-- from interactively supplied or secret-manager-provided credentials.
INSERT INTO cc_rbac_roles (name, description, built_in) VALUES
    ('administrator', 'Full Control Center administration', true),
    ('operator', 'Routine identity and system operations', true),
    ('auditor', 'Read-only security and audit access', true),
    ('viewer', 'Read-only overview access', true)
ON CONFLICT (name) DO NOTHING;

INSERT INTO cc_rbac_permissions (name, description) VALUES
    ('*', 'All permissions; valid only through an explicit role binding'),
    ('system.overview.read', 'Read system overview'),
    ('identity.users.read', 'Read local identities'),
    ('identity.users.write', 'Manage local identities'),
    ('identity.roles.read', 'Read roles and bindings'),
    ('identity.roles.write', 'Manage roles and bindings'),
    ('audit.events.read', 'Read security audit events'),
    ('resources.read', 'Read resource inventory and observed state')
ON CONFLICT (name) DO NOTHING;

INSERT INTO cc_rbac_role_permissions (role_name, permission_name) VALUES
    ('administrator', '*'),
    ('operator', 'system.overview.read'),
    ('operator', 'identity.users.read'),
    ('operator', 'identity.users.write'),
    ('operator', 'identity.roles.read'),
    ('operator', 'resources.read'),
    ('auditor', 'system.overview.read'),
    ('auditor', 'identity.users.read'),
    ('auditor', 'identity.roles.read'),
    ('auditor', 'audit.events.read'),
    ('auditor', 'resources.read'),
    ('viewer', 'system.overview.read'),
    ('viewer', 'resources.read')
ON CONFLICT DO NOTHING;
