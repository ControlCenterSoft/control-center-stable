#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="$(tr -d '\n' < VERSION)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'invalid VERSION: %s\n' "$version" >&2
  exit 2
fi

go_binary="${GO_BINARY:-go}"
commit="${COMMIT:-$(git rev-parse HEAD 2>/dev/null || printf unknown)}"
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
cp -a api/. "$stage/$bundle/api/"
cp config/control-center.env.example "$stage/$bundle/config/"
cp deploy/systemd/control-center.service "$stage/$bundle/deploy/systemd/"
find migrations -maxdepth 1 -type f \( -name '*.sql' -o -name 'README.md' \) \
  -exec cp {} "$stage/$bundle/migrations/" \;
cp scripts/migrate.sh "$stage/$bundle/scripts/"
if [[ -d docs ]]; then
  cp -a docs/. "$stage/$bundle/docs/"
fi
chmod 0755 "$stage/$bundle/bin/control-center" "$stage/$bundle/scripts/migrate.sh"

artifact="$dist_dir/$bundle-linux-amd64.tar.gz"
tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 \
  --numeric-owner -C "$stage" -czf "$artifact" "$bundle"

# Artifact integrity is not enough: the public installer/updater relies on an
# exact runtime package shape. Verify the archive itself before writing a
# checksum so a structurally incomplete payload can never become a qualified
# release asset merely because its digest is internally consistent.
bash scripts/verify-release-archive.sh "$artifact" "$version"

(cd "$dist_dir" && sha256sum "$(basename "$artifact")" > "$(basename "$artifact").sha256")
printf '%s\n' "$artifact" "$artifact.sha256"
