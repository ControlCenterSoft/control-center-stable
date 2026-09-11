# Control Center 0.26.0 Stable

Control Center is a self-contained infrastructure-management control plane. This repository is the official stable distribution channel.

## Current stable release

**0.26.0** is the current stable release. It is promoted from canonical source tag `ControlCenterSoft/control-center-development@v0.26.0`, commit `23c3b971cfeb2081919ffdb88bc0e0bdf6fc6d15`, after independent stable qualification.

0.26.0 adds permission-gated read-only verification of append-only Audit-chain integrity with fail-closed persistence and HTTP boundaries, plus a new compatibility migration for supported legacy PostgreSQL schemas.

## Authentication after a clean install

A clean installation creates local user `admin` with initial password `admin`. The first successful login requires a password change before normal operation is allowed. Upgrading an existing installation preserves the administrator password.

## Start here

- [Installation and upgrade](INSTALL.md)
- [Stable release notes](RELEASE_NOTES.md)
- [Security](SECURITY.md)
- [Architecture](ARCHITECTURE.md)
- [Release manifest](RELEASE-MANIFEST.json)
