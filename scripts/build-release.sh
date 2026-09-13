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
verify_root="$(mktemp -d)"
trap 'rm -rf "$stage" "$verify_root"' EXIT

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

# Publication must fail closed when the archive shape no longer satisfies the
# install/update contract. This verifies the bytes that will actually be
# checksummed and published rather than only the staging directory.
tar -xzf "$artifact" -C "$verify_root"
[[ -x "$verify_root/$bundle/bin/control-center" ]] || {
  printf 'release archive does not contain executable %s/bin/control-center\n' "$bundle" >&2
  exit 3
}
[[ -x "$verify_root/$bundle/scripts/migrate.sh" ]] || {
  printf 'release archive does not contain executable %s/scripts/migrate.sh\n' "$bundle" >&2
  exit 3
}
[[ -f "$verify_root/$bundle/deploy/systemd/control-center.service" ]] || {
  printf 'release archive does not contain %s/deploy/systemd/control-center.service\n' "$bundle" >&2
  exit 3
}
[[ -f "$verify_root/$bundle/VERSION" ]] || {
  printf 'release archive does not contain %s/VERSION\n' "$bundle" >&2
  exit 3
}
[[ "$(tr -d '\r\n' < "$verify_root/$bundle/VERSION")" == "$version" ]] || {
  printf 'release archive VERSION does not match %s\n' "$version" >&2
  exit 3
}

(cd "$dist_dir" && sha256sum "$(basename "$artifact")" > "$(basename "$artifact").sha256")
printf '%s\n' "$artifact" "$artifact.sha256"
