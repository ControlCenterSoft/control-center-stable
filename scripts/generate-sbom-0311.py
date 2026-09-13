#!/usr/bin/env python3
"""Generate the bounded CycloneDX SBOM for the exact 0.31.1 release tree."""

import hashlib
import json
import pathlib
import re
import sys


def fail(message: str) -> None:
    raise SystemExit(message)


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_go_mod(path: pathlib.Path):
    text = path.read_text(encoding="utf-8")
    modules = {}
    in_require_block = False
    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line or line.startswith("//"):
            continue
        if line == "require (":
            in_require_block = True
            continue
        if in_require_block and line == ")":
            in_require_block = False
            continue

        module = None
        version = None
        if line.startswith("require "):
            fields = line.split()
            if len(fields) >= 3:
                module, version = fields[1], fields[2]
        elif in_require_block:
            fields = line.split()
            if len(fields) >= 2:
                module, version = fields[0], fields[1]

        if module is None:
            continue
        if not re.fullmatch(r"v[^\s]+", version or ""):
            fail(f"invalid go.mod require version: {line}")
        if module in modules and modules[module] != version:
            fail(f"conflicting go.mod require versions for {module}")
        modules[module] = version

    if in_require_block:
        fail("unterminated go.mod require block")
    toolchain = re.search(r"^toolchain\s+go([0-9.]+)\s*$", text, re.MULTILINE)
    if not toolchain:
        fail("go.mod toolchain is missing")
    return modules, toolchain.group(1)


def main() -> None:
    if len(sys.argv) != 4:
        fail("usage: generate-sbom-0311.py MANIFEST OUTPUT RELEASE_SHA")

    repo = pathlib.Path(__file__).resolve().parent.parent
    version = (repo / "VERSION").read_text(encoding="ascii").strip()
    if version != "0.31.1":
        fail(f"unexpected release version: {version}")

    manifest_path = pathlib.Path(sys.argv[1])
    if not manifest_path.is_absolute():
        manifest_path = repo / manifest_path
    output_path = pathlib.Path(sys.argv[2])
    if not output_path.is_absolute():
        output_path = repo / output_path
    release_sha = sys.argv[3].strip()
    if not re.fullmatch(r"[0-9a-f]{40}", release_sha):
        fail("release SHA must be exact lowercase 40-hex revision")

    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("schema") != "control-center.third-party-manifest.v1":
        fail("unsupported third-party manifest schema")
    # 0.31.1 is a package-only corrective patch. The dependency/license profile
    # is intentionally inherited from the reviewed 0.31.0 minor-line manifest,
    # while every module and license digest is revalidated against this tree.
    if manifest.get("product_version") != "0.31.0":
        fail("third-party manifest is not the reviewed Control Center 0.31 profile")

    go_modules, toolchain_version = parse_go_mod(repo / "go.mod")
    declared_modules = {}
    components = []
    for item in manifest.get("components", []):
        module = item.get("module", "")
        component_version = item.get("version", "")
        if not module or not component_version:
            fail("third-party manifest contains incomplete component identity")
        if module in declared_modules:
            fail(f"duplicate third-party component: {module}")
        declared_modules[module] = component_version

        license_file = repo / item.get("license_file", "")
        if not license_file.is_file():
            fail(f"license evidence missing for {module}: {license_file}")
        actual_license_sha = sha256_file(license_file)
        if actual_license_sha != item.get("license_sha256"):
            fail(f"license evidence digest mismatch for {module}")

        if module == "go-runtime":
            if component_version != toolchain_version:
                fail(f"Go runtime mismatch: manifest={component_version} go.mod={toolchain_version}")
            component_type = "framework"
        else:
            if go_modules.get(module) != component_version:
                fail(f"go.mod mismatch for {module}: manifest={component_version} go.mod={go_modules.get(module)}")
            component_type = "library"

        bom_ref = f"{module}@{component_version}"
        components.append({
            "type": component_type,
            "bom-ref": bom_ref,
            "name": module,
            "version": component_version,
            "purl": item.get("purl"),
            "licenses": [{"license": {"id": item.get("license_spdx")}}],
            "externalReferences": [{"type": "vcs", "url": item.get("source")}],
            "properties": [
                {"name": "control-center:scope", "value": item.get("scope", "runtime")},
                {"name": "control-center:license-file", "value": item.get("license_file")},
                {"name": "control-center:license-sha256", "value": actual_license_sha},
            ],
        })

    expected_modules = set(go_modules)
    manifest_modules = {module for module in declared_modules if module != "go-runtime"}
    if manifest_modules != expected_modules:
        missing = sorted(expected_modules - manifest_modules)
        extra = sorted(manifest_modules - expected_modules)
        fail(f"third-party manifest/go.mod set mismatch: missing={missing} extra={extra}")

    root_ref = f"pkg:generic/control-center@{version}?revision={release_sha}"
    components.sort(key=lambda component: component["bom-ref"])
    bom = {
        "bomFormat": "CycloneDX",
        "specVersion": "1.7",
        "version": 1,
        "metadata": {
            "component": {
                "type": "application",
                "bom-ref": root_ref,
                "name": "control-center",
                "version": version,
                "properties": [{"name": "control-center:release-sha", "value": release_sha}],
            }
        },
        "components": components,
        "dependencies": [
            {"ref": root_ref, "dependsOn": [component["bom-ref"] for component in components]},
            *[{"ref": component["bom-ref"], "dependsOn": []} for component in components],
        ],
    }
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(json.dumps(bom, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
