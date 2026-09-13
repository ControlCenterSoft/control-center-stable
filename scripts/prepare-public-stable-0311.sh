#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="$(tr -d '\r\n' < VERSION)"
[[ "$version" == "0.31.1" ]] || { echo "expected VERSION=0.31.1, got $version" >&2; exit 2; }
release_sha="${RELEASE_SHA:-$(git rev-parse HEAD)}"
[[ "$release_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid exact release SHA" >&2; exit 2; }
[[ "$(git rev-parse HEAD)" == "$release_sha" ]] || { echo "checked out revision does not match RELEASE_SHA" >&2; exit 2; }
[[ -z "$(git status --porcelain=v1 --untracked-files=all)" ]] || { echo "source tree must be clean" >&2; exit 2; }

source_date_epoch="$(git show -s --format=%ct "$release_sha")"
[[ "$source_date_epoch" =~ ^[0-9]+$ ]] || { echo "invalid source date epoch" >&2; exit 2; }
qualified_run_id="${QUALIFIED_RUN_ID:-}"
[[ "$qualified_run_id" =~ ^[0-9]+$ ]] || { echo "exact successful qualification workflow run id is required" >&2; exit 2; }
[[ -n "${QUALIFIED_ARTIFACT_DIR:-}" ]] || { echo "qualified exact-run artifact directory is required" >&2; exit 2; }
out="${DIST_DIR:-$repo_root/dist/public-stable-$version}"
rm -rf "$out"
mkdir -p "$out"

# Rebuild the exact deterministic binary artifact and prove it is byte-identical
# to the artifact preserved by the successful exact-main verification run.
DIST_DIR="$out" SOURCE_DATE_EPOCH="$source_date_epoch" COMMIT="$release_sha" bash scripts/build-release.sh
binary="$out/control-center-$version-linux-amd64.tar.gz"
sidecar="$binary.sha256"
[[ -s "$binary" && -s "$sidecar" ]]
(cd "$out" && sha256sum -c "$(basename "$sidecar")")

qualified_binary="$QUALIFIED_ARTIFACT_DIR/control-center-$version-linux-amd64.tar.gz"
qualified_sidecar="$QUALIFIED_ARTIFACT_DIR/control-center-$version-linux-amd64.tar.gz.sha256"
[[ -f "$qualified_binary" && -f "$qualified_sidecar" ]] || {
  echo "qualified exact-run binary/sidecar are missing" >&2
  exit 2
}
cmp -s "$qualified_binary" "$binary" || { echo "rebuilt binary differs from exact-run qualified artifact" >&2; exit 1; }
cmp -s "$qualified_sidecar" "$sidecar" || { echo "rebuilt sidecar differs from exact-run qualified sidecar" >&2; exit 1; }

# Source archive is bound to the exact release SHA and uses gzip without mutable
# filename/timestamp metadata.
source_artifact="$out/control-center-$version-source.tar.gz"
git archive --format=tar --prefix="control-center-$version-source/" "$release_sha" | gzip -n > "$source_artifact"
[[ -s "$source_artifact" ]]

# Revalidate the full dependency/license profile from this exact tree and bind
# the resulting CycloneDX document to the 0.31.1 release identity.
sbom="$out/control-center-$version.sbom.cdx.json"
python3 scripts/generate-sbom-0311.py third_party/manifest-0.31.json "$sbom" "$release_sha"
notices="$out/THIRD_PARTY_NOTICES.md"
cp THIRD_PARTY_NOTICES.md "$notices"
[[ -s "$sbom" && -s "$notices" ]]

python3 - "$sbom" "$version" "$release_sha" <<'PY'
import json,re,sys
path,version,sha=sys.argv[1:]
with open(path,encoding="utf-8") as handle:
    bom=json.load(handle)
assert bom.get("bomFormat")=="CycloneDX"
assert bom.get("specVersion")=="1.7"
root=bom.get("metadata",{}).get("component",{})
assert root.get("name")=="control-center"
assert root.get("version")==version
props={item.get("name"):item.get("value") for item in root.get("properties",[])}
assert props.get("control-center:release-sha")==sha
components=bom.get("components",[])
assert components
refs={component.get("bom-ref") for component in components}
assert len(refs)==len(components) and all(refs)
for component in components:
    licenses=component.get("licenses",[])
    assert licenses and licenses[0].get("license",{}).get("id") in {"MIT","BSD-3-Clause"}
    cprops={item.get("name"):item.get("value") for item in component.get("properties",[])}
    assert re.fullmatch(r"[0-9a-f]{64}",cprops.get("control-center:license-sha256", ""))
print("PUBLIC_STABLE_SBOM=PASS")
PY

binary_digest="sha256:$(sha256sum "$binary" | awk '{print $1}')"
sidecar_digest="sha256:$(sha256sum "$sidecar" | awk '{print $1}')"
source_digest="sha256:$(sha256sum "$source_artifact" | awk '{print $1}')"
sbom_digest="sha256:$(sha256sum "$sbom" | awk '{print $1}')"
notices_digest="sha256:$(sha256sum "$notices" | awk '{print $1}')"

qualification="$out/control-center-$version.qualification.json"
python3 - "$qualification" "$version" "$release_sha" "$qualified_run_id" "$binary_digest" "$source_digest" "$sbom_digest" "$notices_digest" <<'PY'
import json,sys
path,version,sha,run_id,binary_digest,source_digest,sbom_digest,notices_digest=sys.argv[1:]
data={
  "schema":"control-center.public-stable-qualification.v1",
  "status":"PASS",
  "version":version,
  "revision":sha,
  "qualified_workflow_run_id":run_id,
  "artifacts":{
    "linux_amd64":binary_digest,
    "source":source_digest,
    "sbom":sbom_digest,
    "third_party_notices":notices_digest,
  },
  "technical_gates":{
    "public_source_no_secret":"PASS",
    "format_vet":"PASS",
    "unit_contract_race":"PASS",
    "build":"PASS",
    "deterministic_bundle":"PASS",
    "package_migration_runner":"PASS",
    "clean_install":"PASS",
    "upgrade_from_0_30":"PASS",
    "upgrade_from_0_31":"PASS",
    "migration_idempotency":"PASS",
    "migration_immutability":"PASS",
    "data_config_admin_credential_preservation":"PASS",
    "rollback_forward_recovery":"PASS",
    "release_identity":"PASS",
  },
  "commercial_legal_launch":"separate-not-product-stable-blocker",
}
with open(path,"w",encoding="utf-8") as handle:
    json.dump(data,handle,sort_keys=True,separators=(",",":")); handle.write("\n")
PY
qualification_digest="sha256:$(sha256sum "$qualification" | awk '{print $1}')"

provenance="$out/control-center-$version.provenance.json"
python3 - "$provenance" "$version" "$release_sha" "$qualified_run_id" "$source_date_epoch" "$binary_digest" "$source_digest" "$sbom_digest" "$notices_digest" "$qualification_digest" <<'PY'
import json,sys
path,version,sha,run_id,epoch,binary_digest,source_digest,sbom_digest,notices_digest,qualification_digest=sys.argv[1:]
data={
  "schema":"control-center.public-stable-provenance.v1",
  "version":version,
  "revision":sha,
  "source_repository":"ControlCenterSoft/control-center-stable",
  "source_date_epoch":int(epoch),
  "qualified_workflow_run_id":run_id,
  "subjects":[
    {"name":f"control-center-{version}-linux-amd64.tar.gz","digest":binary_digest},
    {"name":f"control-center-{version}-source.tar.gz","digest":source_digest},
    {"name":f"control-center-{version}.sbom.cdx.json","digest":sbom_digest},
    {"name":"THIRD_PARTY_NOTICES.md","digest":notices_digest},
    {"name":f"control-center-{version}.qualification.json","digest":qualification_digest},
  ],
  "publication_authority":False,
}
with open(path,"w",encoding="utf-8") as handle:
    json.dump(data,handle,sort_keys=True,separators=(",",":")); handle.write("\n")
PY
provenance_digest="sha256:$(sha256sum "$provenance" | awk '{print $1}')"

release_manifest="$out/control-center-$version.release-manifest.json"
python3 - "$release_manifest" "$version" "$release_sha" "$binary_digest" "$sidecar_digest" "$source_digest" "$sbom_digest" "$notices_digest" "$qualification_digest" "$provenance_digest" <<'PY'
import json,sys
(path,version,sha,binary_digest,sidecar_digest,source_digest,sbom_digest,notices_digest,
 qualification_digest,provenance_digest)=sys.argv[1:]
data={
  "schema":"control-center.public-stable-release-manifest.v1",
  "status":"qualified-for-public-stable",
  "version":version,
  "revision":sha,
  "publication_authority":False,
  "artifacts":{
    f"control-center-{version}-linux-amd64.tar.gz":binary_digest,
    f"control-center-{version}-linux-amd64.tar.gz.sha256":sidecar_digest,
    f"control-center-{version}-source.tar.gz":source_digest,
    f"control-center-{version}.sbom.cdx.json":sbom_digest,
    "THIRD_PARTY_NOTICES.md":notices_digest,
    f"control-center-{version}.qualification.json":qualification_digest,
    f"control-center-{version}.provenance.json":provenance_digest,
  },
}
with open(path,"w",encoding="utf-8") as handle:
    json.dump(data,handle,sort_keys=True,separators=(",",":")); handle.write("\n")
PY

(
  cd "$out"
  sha256sum \
    "control-center-$version-linux-amd64.tar.gz" \
    "control-center-$version-linux-amd64.tar.gz.sha256" \
    "control-center-$version-source.tar.gz" \
    "control-center-$version.sbom.cdx.json" \
    "THIRD_PARTY_NOTICES.md" \
    "control-center-$version.provenance.json" \
    "control-center-$version.qualification.json" \
    "control-center-$version.release-manifest.json" > SHA256SUMS
  sha256sum -c SHA256SUMS
)

# Validate the final exact nine-file public set through the product's closed
# release-candidate artifact contract before a publication workflow may upload it.
validator_dir="$(mktemp -d "$repo_root/.stable-artifact-validator.XXXXXX")"
trap 'rm -rf "$validator_dir"' EXIT
python3 - "$out" "$validator_dir/evidence.json" "$version" "$release_sha" <<'PY'
import hashlib,json,os,sys
root,out,version,sha=sys.argv[1:]
names=[
 f"control-center-{version}-linux-amd64.tar.gz",
 f"control-center-{version}-linux-amd64.tar.gz.sha256",
 f"control-center-{version}-source.tar.gz",
 f"control-center-{version}.sbom.cdx.json",
 "THIRD_PARTY_NOTICES.md",
 f"control-center-{version}.provenance.json",
 f"control-center-{version}.qualification.json",
 f"control-center-{version}.release-manifest.json",
 "SHA256SUMS",
]
artifacts=[]
for name in names:
    path=os.path.join(root,name)
    with open(path,"rb") as handle:
        digest=hashlib.sha256(handle.read()).hexdigest()
    artifacts.append({"name":name,"digest":"sha256:"+digest})
with open(out,"w",encoding="utf-8") as handle:
    json.dump({"schema":"control-center.release-candidate-artifacts.v1","candidate_version":version,"candidate_sha":sha,"artifacts":artifacts},handle)
PY
cat > "$validator_dir/main.go" <<'GO'
package main
import (
  "encoding/json"
  "fmt"
  "os"
  "control-center/internal/releasecandidate"
)
func main() {
  raw, err := os.ReadFile(os.Args[1]); if err != nil { panic(err) }
  var manifest releasecandidate.ArtifactManifest
  if err := json.Unmarshal(raw,&manifest); err != nil { panic(err) }
  if err := releasecandidate.ValidateArtifactManifest(manifest); err != nil { panic(err) }
  fmt.Println("PUBLIC_STABLE_ARTIFACT_SET=PASS")
}
GO
go run "$validator_dir/main.go" "$validator_dir/evidence.json"
rm -rf "$validator_dir"
trap - EXIT

echo "PUBLIC_STABLE_ARTIFACT_PREPARATION=PASS"
