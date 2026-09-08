#!/usr/bin/env bash
set -Eeuo pipefail

readonly REPOSITORY="ControlCenterSoft/control-center-stable"
readonly RAW_BASE="https://raw.githubusercontent.com/${REPOSITORY}/main"
readonly RELEASE_BASE="https://github.com/${REPOSITORY}/releases/download"
readonly INSTALL_ROOT="/opt/control-center"
readonly CURRENT_LINK="${INSTALL_ROOT}/current"
readonly ENV_FILE="/etc/control-center/control-center.env"
readonly STATE_DIR="/var/lib/control-center/auto-update"
readonly BACKUP_DIR="/var/lib/control-center/backups/auto-update"
readonly LOCK_FILE="/run/lock/control-center-auto-update.lock"
readonly SERVICE_NAME="control-center.service"
readonly HEALTH_LIVE="http://127.0.0.1:8080/health/live"
readonly HEALTH_READY="http://127.0.0.1:8080/health/ready"

log() {
  printf '%s %s\n' "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" "$*"
}

fail() {
  log "ERROR: $*" >&2
  exit 1
}

[[ ${EUID:-$(id -u)} -eq 0 ]] || fail "must run as root"

for command_name in curl sha256sum tar systemctl systemd-run pg_dump sort flock readlink install mktemp; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: $command_name"
done

[[ -r "$ENV_FILE" ]] || fail "missing readable environment file: $ENV_FILE"
[[ -L "$CURRENT_LINK" || -d "$CURRENT_LINK" ]] || fail "Control Center is not installed at $CURRENT_LINK"

install -d -o root -g root -m 0755 "$STATE_DIR"
install -d -o control-center -g control-center -m 0700 "$BACKUP_DIR"
install -d -o root -g root -m 0755 "$(dirname "$LOCK_FILE")"

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  log "another update check is already running; exiting"
  exit 0
fi

current_version=""
if [[ -r "$CURRENT_LINK/VERSION" ]]; then
  current_version="$(tr -d '[:space:]' < "$CURRENT_LINK/VERSION")"
fi
[[ "$current_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "cannot determine installed stable version"

latest_version="$(curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 30 "$RAW_BASE/VERSION" | tr -d '[:space:]')"
[[ "$latest_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "stable repository returned an invalid VERSION: $latest_version"

if [[ "$latest_version" == "$current_version" ]]; then
  log "already current: $current_version"
  exit 0
fi

highest_version="$(printf '%s\n%s\n' "$current_version" "$latest_version" | sort -V | tail -n 1)"
if [[ "$highest_version" != "$latest_version" ]]; then
  log "stable repository advertises $latest_version but installed version is $current_version; refusing automatic downgrade"
  exit 0
fi

artifact="control-center-${latest_version}-linux-amd64.tar.gz"
checksum="${artifact}.sha256"
release_url="${RELEASE_BASE}/v${latest_version}"
workdir="$(mktemp -d /tmp/control-center-auto-update.XXXXXX)"
previous_target="$(readlink -f "$CURRENT_LINK")"
previous_unit_backup=""
cleanup() {
  rm -rf "$workdir"
  if [[ -n "$previous_unit_backup" && -f "$previous_unit_backup" ]]; then
    rm -f "$previous_unit_backup"
  fi
}
trap cleanup EXIT

log "update available: $current_version -> $latest_version"
cd "$workdir"
curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 300 \
  --output "$artifact" "$release_url/$artifact"
curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 60 \
  --output "$checksum" "$release_url/$checksum"
sha256sum -c "$checksum"
tar -xzf "$artifact"

source_dir="$workdir/control-center-$latest_version"
[[ -x "$source_dir/bin/control-center" ]] || fail "release payload is missing bin/control-center"
[[ -x "$source_dir/scripts/migrate.sh" ]] || fail "release payload is missing scripts/migrate.sh"
[[ -d "$source_dir/migrations" ]] || fail "release payload is missing migrations"
[[ -f "$source_dir/deploy/systemd/control-center.service" ]] || fail "release payload is missing systemd unit"
[[ -r "$source_dir/VERSION" ]] || fail "release payload is missing VERSION"
payload_version="$(tr -d '[:space:]' < "$source_dir/VERSION")"
[[ "$payload_version" == "$latest_version" ]] || fail "release payload VERSION does not match repository VERSION"

backup_stamp="$(date -u +'%Y%m%dT%H%M%SZ')"
backup_file="$BACKUP_DIR/pre-${current_version}-to-${latest_version}-${backup_stamp}.dump"
pg_dump_bin="$(command -v pg_dump)"
log "creating PostgreSQL backup: $backup_file"
systemd-run --quiet --wait --pipe --collect \
  --service-type=oneshot \
  --uid=control-center \
  --gid=control-center \
  --property=NoNewPrivileges=yes \
  --property=PrivateTmp=yes \
  --property=ProtectSystem=strict \
  --property=ProtectHome=yes \
  --property="EnvironmentFile=$ENV_FILE" \
  -- "$pg_dump_bin" --format=custom --file="$backup_file"
[[ -s "$backup_file" ]] || fail "database backup was not created"

install_dir="$INSTALL_ROOT/$latest_version"
if [[ -e "$install_dir" ]]; then
  [[ -r "$install_dir/VERSION" ]] || fail "existing install directory is incomplete: $install_dir"
  existing_version="$(tr -d '[:space:]' < "$install_dir/VERSION")"
  [[ "$existing_version" == "$latest_version" ]] || fail "existing install directory contains a different version"
else
  install -d -o root -g root -m 0755 "$install_dir"
  cp -a "$source_dir/." "$install_dir/"
  chown -R root:root "$install_dir"
fi

log "stopping $SERVICE_NAME"
systemctl stop "$SERVICE_NAME"

log "applying forward database migrations from $latest_version"
if ! systemd-run --quiet --wait --pipe --collect \
  --service-type=oneshot \
  --uid=control-center \
  --gid=control-center \
  --property=NoNewPrivileges=yes \
  --property=PrivateTmp=yes \
  --property=ProtectSystem=strict \
  --property=ProtectHome=yes \
  --property="EnvironmentFile=$ENV_FILE" \
  --setenv="MIGRATIONS_DIR=$install_dir/migrations" \
  -- "$install_dir/scripts/migrate.sh"; then
  log "migration failed; restarting previous version $current_version"
  systemctl start "$SERVICE_NAME" || true
  fail "migration failed"
fi

if [[ -f /etc/systemd/system/control-center.service ]]; then
  previous_unit_backup="$workdir/control-center.service.previous"
  cp -a /etc/systemd/system/control-center.service "$previous_unit_backup"
fi

install -o root -g root -m 0644 \
  "$install_dir/deploy/systemd/control-center.service" \
  /etc/systemd/system/control-center.service
ln -sfn "$install_dir" "$CURRENT_LINK"
systemctl daemon-reload

log "starting Control Center $latest_version"
if ! systemctl start "$SERVICE_NAME"; then
  log "new service failed to start; reverting application symlink to $previous_target"
  ln -sfn "$previous_target" "$CURRENT_LINK"
  if [[ -n "$previous_unit_backup" && -f "$previous_unit_backup" ]]; then
    cp -a "$previous_unit_backup" /etc/systemd/system/control-center.service
  fi
  systemctl daemon-reload
  systemctl start "$SERVICE_NAME" || true
  fail "new version failed to start; PostgreSQL backup retained at $backup_file"
fi

healthy=0
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error --max-time 3 "$HEALTH_LIVE" >/dev/null 2>&1 \
    && curl --fail --silent --show-error --max-time 3 "$HEALTH_READY" >/dev/null 2>&1; then
    healthy=1
    break
  fi
  sleep 2
done

if [[ "$healthy" -ne 1 ]]; then
  log "health check failed; reverting application symlink to $previous_target"
  systemctl stop "$SERVICE_NAME" || true
  ln -sfn "$previous_target" "$CURRENT_LINK"
  if [[ -n "$previous_unit_backup" && -f "$previous_unit_backup" ]]; then
    cp -a "$previous_unit_backup" /etc/systemd/system/control-center.service
  fi
  systemctl daemon-reload
  systemctl start "$SERVICE_NAME" || true
  fail "new version failed health checks; PostgreSQL backup retained at $backup_file"
fi

cat > "$STATE_DIR/last-success.env" <<STATE
VERSION=$latest_version
PREVIOUS_VERSION=$current_version
UPDATED_AT_UTC=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
BACKUP_FILE=$backup_file
STATE
chmod 0644 "$STATE_DIR/last-success.env"

log "update completed successfully: $current_version -> $latest_version"
