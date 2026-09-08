#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

[[ ${EUID:-$(id -u)} -eq 0 ]] || fail "must run as root"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
updater="$repo_root/scripts/stable-auto-update.sh"
service_unit="$repo_root/deploy/systemd/control-center-auto-update.service"
timer_unit="$repo_root/deploy/systemd/control-center-auto-update.timer"

[[ -f "$updater" ]] || fail "missing updater: $updater"
[[ -f "$service_unit" ]] || fail "missing service unit: $service_unit"
[[ -f "$timer_unit" ]] || fail "missing timer unit: $timer_unit"
[[ -f /etc/control-center/control-center.env ]] || fail "Control Center environment file is missing"
[[ -e /opt/control-center/current ]] || fail "Control Center is not installed"
command -v pg_dump >/dev/null 2>&1 || fail "pg_dump is required for safe automatic updates"

install -o root -g root -m 0755 "$updater" /usr/local/sbin/control-center-stable-update
install -o root -g root -m 0644 "$service_unit" /etc/systemd/system/control-center-auto-update.service
install -o root -g root -m 0644 "$timer_unit" /etc/systemd/system/control-center-auto-update.timer
install -d -o root -g root -m 0755 /var/lib/control-center/auto-update
install -d -o control-center -g control-center -m 0700 /var/lib/control-center/backups/auto-update

systemctl daemon-reload
systemctl enable --now control-center-auto-update.timer
systemctl start control-center-auto-update.service

printf '\nAutomatic stable updates are enabled.\n'
printf 'Timer:  control-center-auto-update.timer\n'
printf 'Status: systemctl status control-center-auto-update.timer\n'
printf 'Logs:   journalctl -u control-center-auto-update.service\n'
