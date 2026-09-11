# Database migrations

Migrations are ordered and immutable once merged. The current cumulative schema is `0001` through `0009` and covers the 0.3 resource, identity, RBAC, audit, persistence and Change/Job baseline; CAS-versioned distributed core objects; persistent Network Zone/Interface contract types; additive built-in RBAC synchronization; and durable authentication-session activity tracking.

Migration `0006` is additive for supported 0.3 installations: it preserves the legacy `organizations`, `resources`, and `config_revisions` tables, adds `cc_core_objects` plus durable idempotency receipts, and creates the single canonical `global` scope when no distributed topology existed before the upgrade.

Migration `0007` extends only the closed `cc_core_objects.object_type` constraint with `network-zone` and `network-interface`. It neither rewrites legacy 0.3 data nor activates network behavior. Its downgrade fails closed while either new object type exists; the operator must explicitly export or remove those objects before reverting.

Migration `0008` synchronizes the persisted permissions and built-in role grants with `internal/identity/rbac`. It never updates an existing permission definition. A small insertion ledger lets the scoped down migration remove only rows introduced by `0008`, retaining pre-existing custom permissions and grants even when they use a name that later becomes built in.

Migration `0009` adds `cc_auth_sessions.last_activity_at`, backfills existing sessions from their immutable creation time, enforces `created_at <= last_activity_at <= expires_at`, and adds a partial user/activity index for non-revoked session inventory. It does not extend an existing session's absolute expiry and does not modify credential material.

The cumulative schema requires PostgreSQL 15 or newer because the immutable `0003` migration uses `NULLS NOT DISTINCT` uniqueness semantics. The qualification suite covers PostgreSQL 15 through 18, creates its own randomly named disposable databases, verifies clean install and supported upgrade/downgrade paths, and removes only those databases. It checks legacy Node/Market resources, administrator credentials and sessions, RBAC, the append-only audit chain, durable Change state, deterministic core bootstrap, migration replay, and connection restart behavior. `MIGRATION_INSTALL_DATABASE_URL` is a separate explicit gate for installing the current schema into an empty disposable database before adapter/race tests.

Apply migrations only to an explicitly selected development/test database. Never place database credentials in this repository.
