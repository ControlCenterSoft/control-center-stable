# Control Center 0.26.0 Stable

Release status: **stable**.

Control Center 0.26.0 adds permission-gated read-only verification of append-only Audit-chain integrity. The successful response exposes only bounded aggregate evidence, detects tampering or broken links, fails closed on read/decode/hash/link errors, and records successful verification in Audit before returning success.

The release also adds migration `0010_legacy_03_schema_compatibility` for supported legacy PostgreSQL schemas without rewriting previously published migration files.

Independent stable qualification passed provenance/public-safety checks, format/vet/unit/contracts/build, PostgreSQL 15–18 clean-install and supported-upgrade scenarios, PostgreSQL adapter/restart checks, race detection, deterministic Linux AMD64 packaging and the final stable qualification gate.

Clean install uses `admin` / `admin` with mandatory password change on first login. Upgrade preserves the existing administrator password.
