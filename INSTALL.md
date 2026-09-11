# Control Center 0.26.0 — installation and upgrade

## Requirements

- Linux AMD64 with systemd;
- PostgreSQL 15, 16, 17 or 18;
- `psql` for database migrations;
- TLS termination in front of Control Center for production access.

## Clean installation

1. Download `control-center-0.26.0-linux-amd64.tar.gz` and verify it with the matching `.sha256` file or `SHA256SUMS`.
2. Extract the bundle under `/opt/control-center/releases/0.26.0` and point `/opt/control-center/current` to that directory.
3. Create the `control-center` service account and `/var/lib/control-center` working directory.
4. Copy `config/control-center.env.example` to `/etc/control-center/control-center.env`, set the PostgreSQL connection and restrict file permissions.
5. Back up PostgreSQL if it already contains data, then apply forward migrations through `scripts/migrate.sh`.
6. Install `deploy/systemd/control-center.service`, reload systemd and start the service.
7. Sign in as `admin` / `admin` and immediately complete the mandatory first-login password change.

The supplied configuration binds to loopback by default. Do not expose the bootstrap credential on an untrusted network.

## Upgrade from an earlier stable release

1. Back up PostgreSQL and the current Control Center configuration.
2. Stop the service.
3. Extract 0.26.0 into a new release directory.
4. Apply forward migrations with the existing database credentials.
5. Move `/opt/control-center/current` to 0.26.0 and start the service.
6. Verify readiness, existing administrator access, Audit access according to RBAC, and critical managed-resource reads.

The existing administrator password is preserved during upgrade and is not reset to `admin`.
