# Control Center 0.25.0 Stable

Release status: **stable**.

Control Center 0.25.0 adds permission-gated read-only access to Audit events while preserving the append-only integrity model.

- `GET /api/v1/audit/events` requires an authenticated identity with global `audit.events.read` permission and completion of the mandatory initial-password change.
- Reads use bounded pagination, an opaque cursor and exact filters for action, outcome, actor and subject.
- Responses are returned newest-first with `Cache-Control: no-store` and safe structured-detail rendering.
- PostgreSQL-backed reads validate stored event integrity before returning data and fail closed on decode or integrity errors.
- Successful privileged reads create Audit evidence; if that evidence cannot be recorded, events are not returned.
- In-memory and PostgreSQL readers share the same contract and HTTP behavior.

The stable release retains the existing clean-install authentication model, preserves administrator passwords during upgrades, and supports PostgreSQL 15–18.
