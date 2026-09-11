BEGIN;

-- Record only rows introduced by this migration so a deliberate schema
-- downgrade can remove its own grants without deleting pre-existing custom
-- permissions or grants that happen to use the same names.
CREATE TABLE IF NOT EXISTS cc_migration_0008_rbac_seed (
    item_kind varchar(16) NOT NULL CHECK (item_kind IN ('permission', 'role-grant')),
    role_name varchar(128) NOT NULL,
    permission_name varchar(192) NOT NULL,
    PRIMARY KEY (item_kind, role_name, permission_name),
    CHECK (
        (item_kind = 'permission' AND role_name = '') OR
        (item_kind = 'role-grant' AND role_name <> '')
    )
);

WITH desired(name, description) AS (
    VALUES
        ('*', 'All permissions; valid only through an explicit role binding'),
        ('system.overview.read', 'Read system overview'),
        ('identity.users.read', 'Read local identities'),
        ('identity.users.write', 'Manage local identities'),
        ('identity.roles.read', 'Read roles and bindings'),
        ('identity.roles.write', 'Manage roles and bindings'),
        ('audit.events.read', 'Read security audit events'),
        ('resources.read', 'Read resource inventory and observed state'),
        ('config.revisions.write', 'Create immutable configuration revisions'),
        ('orchestration.actions.read', 'Read executable action descriptors'),
        ('orchestration.changes.write', 'Create changes'),
        ('orchestration.changes.approve', 'Approve high-risk changes'),
        ('orchestration.jobs.read', 'Read job execution state and outputs'),
        ('orchestration.jobs.cancel', 'Request job cancellation'),
        ('orchestration.actions.execute', 'Execute allowlisted orchestration actions'),
        ('nodes.enrollment.plan', 'Plan node enrollment'),
        ('nodes.lifecycle.read', 'Read node lifecycle state'),
        ('nodes.lifecycle.plan', 'Plan guarded node lifecycle transitions'),
        ('automation.plan', 'Plan software automation without host mutation'),
        ('pxe.plan', 'Plan PXE deployment without host mutation'),
        ('market.manifests.read', 'Read Market manifests'),
        ('domain.provider.resolve', 'Resolve compatible directory-service providers'),
        ('domain.lifecycle.plan', 'Plan directory-service lifecycle operations'),
        ('inventory.normalize', 'Normalize inventory observations'),
        ('agent.enrollment.normalize', 'Normalize agent enrollment contracts'),
        ('inventory.reconcile', 'Reconcile inventory observations'),
        ('inventory.freshness.evaluate', 'Evaluate inventory freshness'),
        ('agent.heartbeat.evaluate', 'Evaluate agent heartbeat health'),
        ('agent.lease.evaluate', 'Evaluate agent lease state'),
        ('core.objects.read', 'Read distributed core topology and state objects'),
        ('core.objects.write', 'Apply typed distributed core object changes through Change and Job')
), inserted AS (
    INSERT INTO cc_rbac_permissions (name, description)
    SELECT name, description FROM desired
    ON CONFLICT (name) DO NOTHING
    RETURNING name
)
INSERT INTO cc_migration_0008_rbac_seed (item_kind, role_name, permission_name)
SELECT 'permission', '', name FROM inserted
ON CONFLICT DO NOTHING;

WITH desired(role_name, permission_name) AS (
    VALUES
        ('administrator', '*'),
        ('operator', 'system.overview.read'),
        ('operator', 'identity.users.read'),
        ('operator', 'identity.users.write'),
        ('operator', 'identity.roles.read'),
        ('operator', 'resources.read'),
        ('operator', 'config.revisions.write'),
        ('operator', 'orchestration.actions.read'),
        ('operator', 'orchestration.changes.write'),
        ('operator', 'orchestration.jobs.read'),
        ('operator', 'orchestration.jobs.cancel'),
        ('operator', 'nodes.enrollment.plan'),
        ('operator', 'nodes.lifecycle.read'),
        ('operator', 'nodes.lifecycle.plan'),
        ('operator', 'automation.plan'),
        ('operator', 'pxe.plan'),
        ('operator', 'market.manifests.read'),
        ('operator', 'domain.provider.resolve'),
        ('operator', 'domain.lifecycle.plan'),
        ('operator', 'inventory.normalize'),
        ('operator', 'agent.enrollment.normalize'),
        ('operator', 'inventory.reconcile'),
        ('operator', 'inventory.freshness.evaluate'),
        ('operator', 'agent.heartbeat.evaluate'),
        ('operator', 'agent.lease.evaluate'),
        ('operator', 'core.objects.read'),
        ('operator', 'core.objects.write'),
        ('auditor', 'system.overview.read'),
        ('auditor', 'audit.events.read'),
        ('auditor', 'identity.users.read'),
        ('auditor', 'identity.roles.read'),
        ('auditor', 'resources.read'),
        ('auditor', 'orchestration.actions.read'),
        ('auditor', 'orchestration.jobs.read'),
        ('auditor', 'market.manifests.read'),
        ('auditor', 'nodes.lifecycle.read'),
        ('auditor', 'core.objects.read'),
        ('viewer', 'system.overview.read'),
        ('viewer', 'resources.read'),
        ('viewer', 'orchestration.actions.read'),
        ('viewer', 'market.manifests.read'),
        ('viewer', 'nodes.lifecycle.read'),
        ('viewer', 'core.objects.read')
), inserted AS (
    INSERT INTO cc_rbac_role_permissions (role_name, permission_name)
    SELECT role_name, permission_name FROM desired
    ON CONFLICT DO NOTHING
    RETURNING role_name, permission_name
)
INSERT INTO cc_migration_0008_rbac_seed (item_kind, role_name, permission_name)
SELECT 'role-grant', role_name, permission_name FROM inserted
ON CONFLICT DO NOTHING;

COMMIT;
