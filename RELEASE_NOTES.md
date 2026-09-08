# Control Center 0.3.1 release notes

Release status: stable

## Corrections

- The overview API and browser page now report the runtime build version
  instead of a stale hard-coded value.
- Source-build version metadata now defaults to the same version as `VERSION`.
- Database migrations are launched through a transient systemd service that
  reads the service `EnvironmentFile` directly; installation no longer sources
  that file in a privileged shell.
- Database credential guidance now defines one logical password and its exact
  raw-versus-percent-encoded representation.

## API compatibility

This patch preserves the `/api/v1` contract from 0.3.0. Output evidence remains
bounded and typed; unknown top-level or nested evidence fields are rejected.

## Database

There are no schema changes from 0.3.0. The migration floor remains
`0004_change_execution_core.up.sql`; the checksum-tracked runner is idempotent.
Back up the database before every upgrade.

## Known limitations

- Installation and upgrade are operator-driven.
- TLS termination is provided by the deployment environment.
- The bootstrap administrator secret remains required at service startup and
  must be protected as a long-lived secret.
