#!/usr/bin/env bash
set -Eeuo pipefail

archive="${1:-}"
expected_version="${2:-}"

if [[ -z "$archive" || -z "$expected_version" ]]; then
  echo "usage: $0 <control-center-VERSION-linux-amd64.tar.gz> <VERSION>" >&2
  exit 64
fi
if [[ ! -f "$archive" ]]; then
  echo "release archive not found: $archive" >&2
  exit 66
fi
if [[ ! "$expected_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid expected version: $expected_version" >&2
  exit 64
fi

root="control-center-$expected_version"
members_file="$(mktemp)"
work="$(mktemp -d)"
trap 'rm -f "$members_file"; rm -rf "$work"' EXIT

tar -tzf "$archive" > "$members_file"

# Every member must stay under the one exact versioned top-level directory.
while IFS= read -r member; do
  [[ -n "$member" ]] || continue
  case "$member" in
    "$root"|"$root/"|"$root/"*) ;;
    *)
      echo "release archive contains member outside exact root: $member" >&2
      exit 66
      ;;
  esac
  if [[ "$member" == /* || "$member" == *"/../"* || "$member" == ../* || "$member" == *"/.." ]]; then
    echo "release archive contains unsafe path: $member" >&2
    exit 66
  fi
done < "$members_file"

required=(
  "$root/VERSION"
  "$root/bin/control-center"
  "$root/scripts/migrate.sh"
  "$root/config/control-center.env.example"
  "$root/deploy/systemd/control-center.service"
  "$root/migrations/README.md"
  "$root/RELEASE-MANIFEST.json"
  "$root/INSTALL.md"
  "$root/SECURITY.md"
)
for member in "${required[@]}"; do
  if ! grep -Fxq "$member" "$members_file"; then
    echo "release archive missing required member: $member" >&2
    exit 66
  fi
done

# Migration payload must contain at least one numbered up migration. The updater
# cannot safely infer a successful package from a README-only migrations tree.
if ! grep -Eq "^${root}/migrations/[0-9]{4}_[^/]+\.up\.sql$" "$members_file"; then
  echo "release archive contains no numbered up migration" >&2
  exit 66
fi

tar -xzf "$archive" -C "$work" \
  "$root/VERSION" \
  "$root/bin/control-center" \
  "$root/scripts/migrate.sh"

actual_version="$(tr -d '[:space:]' < "$work/$root/VERSION")"
if [[ "$actual_version" != "$expected_version" ]]; then
  echo "archive VERSION mismatch: got $actual_version expected $expected_version" >&2
  exit 66
fi
if [[ ! -x "$work/$root/bin/control-center" ]]; then
  echo "release binary is not executable" >&2
  exit 66
fi
if [[ ! -x "$work/$root/scripts/migrate.sh" ]]; then
  echo "migration runner is missing executable mode" >&2
  exit 66
fi

printf 'RELEASE_ARCHIVE_SHAPE=PASS version=%s archive=%s\n' "$expected_version" "$archive"
