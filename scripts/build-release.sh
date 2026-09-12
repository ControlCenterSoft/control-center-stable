#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="$(tr -d '\n' < VERSION)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'invalid VERSION: %s\n' "$version" >&2
  exit 2
fi

command -v python3 >/dev/null 2>&1 || { printf 'python3 is required to bind release identity\n' >&2; exit 2; }
manifest_commit="$(python3 - RELEASE-MANIFEST.json "$version" <<'PY'
import json
import re
import sys

path, version = sys.argv[1:]
with open(path, encoding="utf-8") as stream:
    data = json.load(stream)
commit = data.get("source_commit")
if data.get("schema") != "control-center.stable-release.v1":
    raise SystemExit("invalid release manifest schema")
if data.get("channel") != "stable":
    raise SystemExit("invalid release manifest channel")
if data.get("version") != version or data.get("source_tag") != f"v{version}":
    raise SystemExit("release manifest version/tag mismatch")
if re.fullmatch(r"[0-9a-f]{40}", commit or "") is None:
    raise SystemExit("invalid release manifest source_commit")
print(commit)
PY
)"

go_binary="${GO_BINARY:-go}"
commit="${COMMIT:-$manifest_commit}"
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'invalid COMMIT: %s\n' "$commit" >&2
  exit 2
fi
if [[ "$commit" != "$manifest_commit" ]]; then
  printf 'COMMIT does not match RELEASE-MANIFEST source_commit: %s != %s\n' "$commit" "$manifest_commit" >&2
  exit 2
fi
source_date_epoch="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD 2>/dev/null || printf 0)}"
if [[ ! "$source_date_epoch" =~ ^[0-9]+$ ]]; then
  printf 'invalid SOURCE_DATE_EPOCH\n' >&2
  exit 2
fi
build_time="$(date -u -d "@$source_date_epoch" +%Y-%m-%dT%H:%M:%SZ)"

dist_dir="${DIST_DIR:-$repo_root/dist}"
mkdir -p "$dist_dir"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

bundle="control-center-$version"
mkdir -p \
  "$stage/$bundle/bin" \
  "$stage/$bundle/api" \
  "$stage/$bundle/config" \
  "$stage/$bundle/deploy/systemd" \
  "$stage/$bundle/migrations" \
  "$stage/$bundle/scripts" \
  "$stage/$bundle/docs"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_binary" build \
  -trimpath -buildvcs=false \
  -ldflags="-s -w \
    -X control-center/internal/buildinfo.Version=$version \
    -X control-center/internal/buildinfo.Commit=$commit \
    -X control-center/internal/buildinfo.BuildTime=$build_time" \
  -o "$stage/$bundle/bin/control-center" ./cmd/control-center

cp README.md INSTALL.md RELEASE_NOTES.md SECURITY.md ARCHITECTURE.md ROADMAP.md \
  VERSION RELEASE-MANIFEST.json "$stage/$bundle/"
printf '%s\n' "$commit" > "$stage/$bundle/REVISION"
cp -a api/. "$stage/$bundle/api/"
cp config/control-center.env.example "$stage/$bundle/config/"
cp deploy/systemd/control-center.service \
  deploy/systemd/control-center-auto-update.service \
  deploy/systemd/control-center-auto-update.timer \
  "$stage/$bundle/deploy/systemd/"
find migrations -maxdepth 1 -type f \( -name '*.sql' -o -name 'README.md' \) \
  -exec cp {} "$stage/$bundle/migrations/" \;
cp scripts/migrate.sh \
  scripts/configure-public-https.sh \
  scripts/stable-auto-update.sh \
  scripts/install-stable-auto-update.sh \
  scripts/verify-stable-auto-update.sh \
  "$stage/$bundle/scripts/"
if [[ -d docs ]]; then
  cp -a docs/. "$stage/$bundle/docs/"
fi
chmod 0755 "$stage/$bundle/bin/control-center" \
  "$stage/$bundle/scripts/migrate.sh" \
  "$stage/$bundle/scripts/configure-public-https.sh" \
  "$stage/$bundle/scripts/stable-auto-update.sh" \
  "$stage/$bundle/scripts/install-stable-auto-update.sh" \
  "$stage/$bundle/scripts/verify-stable-auto-update.sh"

artifact="$dist_dir/$bundle-linux-amd64.tar.gz"
tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 \
  --numeric-owner -C "$stage" -czf "$artifact" "$bundle"
(cd "$dist_dir" && sha256sum "$(basename "$artifact")" > "$(basename "$artifact").sha256")
printf '%s\n' "$artifact" "$artifact.sha256"
