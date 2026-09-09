#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'must run as root'
DOMAIN="${1:-}"
EXPECTED_IPV4="${2:-}"
[[ -n "$DOMAIN" ]] || fail 'domain argument is required'
[[ -n "$EXPECTED_IPV4" ]] || fail 'expected IPv4 argument is required'

resolved="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk 'NR==1{print $1}')"
[[ "$resolved" == "$EXPECTED_IPV4" ]] || fail "DNS mismatch: $resolved"

command -v nginx >/dev/null 2>&1 || fail 'nginx is required'
command -v certbot >/dev/null 2>&1 || fail 'certbot is required'

curl --fail --silent --show-error --max-time 5 -H "Host: $DOMAIN" http://127.0.0.1/health/ready >/dev/null || fail 'HTTP reverse proxy is not ready'

ufw allow 80/tcp comment 'Control Center portal HTTP' >/dev/null 2>&1 || true

certbot --nginx -d "$DOMAIN" --redirect --non-interactive --agree-tos --register-unsafely-without-email

[[ -s "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ]] || fail 'certificate was not created'
nginx -t >/dev/null
systemctl reload nginx
ufw allow 443/tcp comment 'Control Center portal HTTPS' >/dev/null

curl --fail --silent --show-error --max-time 10 --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/health/ready" >/dev/null || fail 'local HTTPS acceptance failed'

printf 'PUBLIC_HTTPS=PASS\n'
printf 'URL=https://%s\n' "$DOMAIN"
printf 'DNS=%s\n' "$resolved"
printf 'NGINX=%s\n' "$(systemctl is-active nginx)"
printf 'CERTIFICATE=PASS\n'
printf 'HTTPS_FIREWALL=PASS\n'
