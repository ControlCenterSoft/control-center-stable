#!/usr/bin/env bash
set -Eeuo pipefail

release_tag="${1:-}"
repository="${2:-${GITHUB_REPOSITORY:-ControlCenterSoft/control-center-stable}}"

if [[ ! "$release_tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH [owner/repo]" >&2
  exit 64
fi
version="${BASH_REMATCH[1]}"
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "invalid repository: $repository" >&2
  exit 64
fi
command -v gh >/dev/null 2>&1 || { echo "gh CLI is required" >&2; exit 69; }

release_state="$(gh release view "$release_tag" --repo "$repository" --json tagName,isDraft,isPrerelease --jq '[.tagName,.isDraft,.isPrerelease]|@tsv')"
IFS=$'\t' read -r actual_tag is_draft is_prerelease <<< "$release_state"
if [[ "$actual_tag" != "$release_tag" || "$is_draft" != false || "$is_prerelease" != false ]]; then
  echo "release is not an exact non-draft non-prerelease Stable: $release_state" >&2
  exit 66
fi

artifact="control-center-${version}-linux-amd64.tar.gz"
sidecar="${artifact}.sha256"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

gh release download "$release_tag" --repo "$repository" \
  --pattern "$artifact" \
  --pattern "$sidecar" \
  --dir "$work"

[[ -f "$work/$artifact" ]] || { echo "published runtime artifact missing" >&2; exit 66; }
[[ -f "$work/$sidecar" ]] || { echo "published checksum sidecar missing" >&2; exit 66; }
(
  cd "$work"
  sha256sum -c "$sidecar"
)

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bash "$repo_root/scripts/verify-release-archive.sh" "$work/$artifact" "$version"

printf 'PUBLISHED_RELEASE_ASSET=PASS tag=%s repository=%s\n' "$release_tag" "$repository"
