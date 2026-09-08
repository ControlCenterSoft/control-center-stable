# Control Center 0.3.0 release notes

Release status: stable

## Highlights

- Immutable configuration revisions with stale-write preconditions.
- Policy decisions, risk classes, independent approvals, and an explicit
  Change state machine.
- PostgreSQL-backed durable jobs, leases, retries, cancellation, and
  idempotency records.
- A typed Action Registry and allowlisted worker with mandatory post-action
  verification.
- Actual State, Health, and action-audit evidence persisted with job results.
- A fail-closed output-integrity gate. Successful output requires a one-to-one
  Actual State and Health view; invalid or unbounded provider output is
  rejected before commit.
- Closed OpenAPI schemas for action outputs and their nested evidence.

## API compatibility

The release extends `/api/v1` with configuration revisions, changes,
approvals, actions, jobs, and cancellation. Output evidence uses bounded,
typed contracts; unknown top-level or nested evidence fields are rejected.

## Database

Apply PostgreSQL migrations through
`0004_change_execution_core.up.sql` before starting 0.3.0. Back up the
database first.

## Known limitations

- Installation and upgrade are operator-driven.
- TLS termination is provided by the deployment environment.
- The bootstrap administrator secret remains required at service startup and
  must be protected as a long-lived secret.
