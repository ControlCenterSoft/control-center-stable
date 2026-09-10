#!/usr/bin/env bash
set -Eeuo pipefail

[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo 'VERIFY=FAIL reason=must_be_root' >&2; exit 1; }
[[ -x /usr/local/sbin/control-center-stable-update ]] || { echo 'VERIFY=FAIL reason=updater_missing' >&2; exit 1; }
[[ "$(systemctl is-enabled control-center-auto-update.timer 2>/dev/null || true)" == enabled ]] || { echo 'VERIFY=FAIL reason=timer_not_enabled' >&2; exit 1; }
[[ "$(systemctl is-active control-center-auto-update.timer 2>/dev/null || true)" == active ]] || { echo 'VERIFY=FAIL reason=timer_not_active' >&2; exit 1; }
[[ "$(systemctl is-active control-center.service 2>/dev/null || true)" == active ]] || { echo 'VERIFY=FAIL reason=control_center_not_active' >&2; exit 1; }

printf 'VERIFY=PASS\n'
printf 'VERSION=%s\n' "$(tr -d '[:space:]' </opt/control-center/current/VERSION)"
printf 'NEXT_CHECK=%s\n' "$(systemctl show control-center-auto-update.timer -p NextElapseUSecRealtime --value)"
