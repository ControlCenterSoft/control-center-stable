# Control Center 0.3.1

Control Center is an infrastructure-management control plane with durable
identity, policy, audit, configuration revision, change, job, and worker
boundaries.

## Release channels

This repository is the **stable binary channel**. Version `0.3.1` is the
current stable binary release.

The newer official source-release line is published separately in
[`ControlCenterSoft/control-center-development`](https://github.com/ControlCenterSoft/control-center-development),
where the latest officially published source release is `0.24.0`.

Do not assume that features introduced after `0.3.1` in the source-release
line are available in this stable binary channel until a corresponding stable
build is separately qualified and published here.

## Included

- local administrator authentication and PostgreSQL-backed sessions;
- role-based access control and append-only audit records;
- immutable configuration revisions and stale-write protection;
- policy decisions, risk classes, approvals, and a typed Change state machine;
- durable jobs with leases, retries, cancellation, and idempotency;
- an allowlisted worker with post-action verification;
- bounded Actual State, Health, and audit evidence;
- fail-closed validation and canonical persistence of provider output;
- versioned OpenAPI contracts and checksum-tracked database migrations.

The API fails closed when PostgreSQL is unavailable. The default deployment
binds to loopback and is intended to sit behind an operator-managed TLS reverse
proxy.

## Start here

- [Installation](INSTALL.md)
- [Release notes](RELEASE_NOTES.md)
- [Security](SECURITY.md)
- [Architecture](ARCHITECTURE.md)
- [OpenAPI 0.3](api/openapi-0.3.yaml)

## Build from source

Requirements: Go 1.23 or newer.

```sh
make ci
bash scripts/build-release.sh
```

The release builder writes the Linux AMD64 bundle and checksum to `dist/`.
