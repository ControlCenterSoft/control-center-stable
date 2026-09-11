# Control Center 0.25.0 Stable

Control Center is a self-contained infrastructure-management control plane. This repository is the official stable distribution channel.

## Current stable release

**0.25.0** is the current stable release. It is mapped to canonical release `v0.25.0` at commit `1663799629e713e9c2432d90c3b4fd0856d89ad4` and to qualified candidate commit `179f7b13315d86e01bd25c0e5be9619d79c5a631`.

The public stable tree contains the approved product source plus stable-only packaging, installation and service files. Public downloadable artifacts are published with SHA-256 checksums and provenance metadata.

## Authentication after a clean install

A clean installation creates local user `admin` with initial password `admin`. The first successful login requires a password change before normal operation is allowed. Upgrading an existing installation does not reset the administrator password.

## Start here

- [Installation and upgrade](INSTALL.md)
- [Stable release notes](RELEASE_NOTES.md)
- [Security](SECURITY.md)
- [Architecture](ARCHITECTURE.md)
- [Release manifest](RELEASE-MANIFEST.json)
