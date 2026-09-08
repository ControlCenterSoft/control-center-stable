# Install Control Center 0.3.1

## Requirements

- Linux on AMD64 with systemd and `systemd-run`;
- PostgreSQL 17 or a compatible supported PostgreSQL service;
- `psql`, `sha256sum`, `curl`, and `tar`;
- an HTTPS reverse proxy for browser or remote access.

## Download and verify

```sh
version=0.3.1
curl -fL -o "control-center-$version-linux-amd64.tar.gz" \
  "https://github.com/ControlCenterSoft/control-center-stable/releases/download/v$version/control-center-$version-linux-amd64.tar.gz"
curl -fL -o "control-center-$version-linux-amd64.tar.gz.sha256" \
  "https://github.com/ControlCenterSoft/control-center-stable/releases/download/v$version/control-center-$version-linux-amd64.tar.gz.sha256"
sha256sum -c "control-center-$version-linux-amd64.tar.gz.sha256"
tar -xzf "control-center-$version-linux-amd64.tar.gz"
```

Stop if checksum verification fails.

## Install files

```sh
sudo useradd --system --home-dir /var/lib/control-center \
  --create-home --shell /usr/sbin/nologin control-center 2>/dev/null || true
sudo install -d -o root -g root -m 0755 /opt/control-center/0.3.1
sudo cp -a control-center-0.3.1/. /opt/control-center/0.3.1/
sudo ln -sfn /opt/control-center/0.3.1 /opt/control-center/current
sudo install -d -o root -g root -m 0755 /etc/control-center
sudo install -o root -g root -m 0600 \
  /opt/control-center/current/config/control-center.env.example \
  /etc/control-center/control-center.env
sudo install -o root -g root -m 0644 \
  /opt/control-center/current/deploy/systemd/control-center.service \
  /etc/systemd/system/control-center.service
```

Edit `/etc/control-center/control-center.env`. Replace every
`replace-with-...` value and the example database host. Keep the file owned
by root with mode `0600`.

Use one logical database password for both connection forms. The recommended
single-literal form is at least 32 characters drawn only from the URI-unreserved
set `A-Z`, `a-z`, `0-9`, `.`, `_`, `~`, and `-`. With that form, put the exact
same value in `PGPASSWORD` and in the password component of `CC_DATABASE_URL`.

If an existing database password contains any other character, keep the raw
password in `PGPASSWORD` and percent-encode its UTF-8 bytes exactly once in the
`CC_DATABASE_URL` password component. These are two representations of the
same password; do not generate separate values and do not percent-encode
`PGPASSWORD`.

## Prepare the database

Create an empty database and role using your PostgreSQL administration
procedure. Then apply all forward migrations in a transient service. systemd
reads the same `EnvironmentFile` as the application without evaluating it as
shell code:

```sh
sudo systemd-run --wait --pipe --collect \
  --service-type=oneshot \
  --uid=control-center \
  --gid=control-center \
  --property=NoNewPrivileges=yes \
  --property=PrivateTmp=yes \
  --property=ProtectSystem=strict \
  --property=ProtectHome=yes \
  --property=EnvironmentFile=/etc/control-center/control-center.env \
  --setenv=MIGRATIONS_DIR=/opt/control-center/current/migrations \
  -- /opt/control-center/current/scripts/migrate.sh
```

The migration runner records a checksum for every applied migration and stops
if an already-applied file has changed.

## Start and verify

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now control-center
curl --fail --silent --show-error http://127.0.0.1:8080/health/live
curl --fail --silent --show-error http://127.0.0.1:8080/health/ready
```

Publish the loopback listener only through an HTTPS reverse proxy. Restrict
ingress to authorized operators and do not expose PostgreSQL publicly.

## Upgrade and rollback

Back up PostgreSQL before every upgrade. To upgrade, stop the service, install
the new version in a new directory, apply its forward migrations, update the
`current` symlink, and restart.

For rollback, stop the service and restore the matching database backup before
repointing `current` to the earlier version. Do not run a down migration on a
live database without first confirming that 0.3-only orchestration state may be
discarded.
