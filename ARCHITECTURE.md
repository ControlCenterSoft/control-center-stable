# Control Center architecture

Control Center is a single Go control-plane service backed by PostgreSQL.

## Runtime boundaries

- **HTTP/API:** health, version, resources, identity, configuration revisions,
  changes, approvals, actions, and jobs.
- **Identity:** local users, password verification, sessions, RBAC, and
  append-only audit records.
- **Orchestration:** immutable revisions, policy evaluation, Change state,
  durable Job state, idempotency, leases, retries, and cancellation.
- **Worker:** executes only registered actions and verifies observed results
  before marking a job successful.
- **Persistence:** PostgreSQL stores identity, audit, revisions, changes, jobs,
  outputs, leases, and idempotency records.

## Failure model

Startup and readiness fail closed without a reachable migrated database.
Provider output is bounded and validated before verification or persistence.
Successful output must contain one Actual State and one Health record for each
affected resource. Persisted output is validated again when read.

The API process contains no privileged infrastructure credentials. External
providers remain behind the action registry and worker boundary.
