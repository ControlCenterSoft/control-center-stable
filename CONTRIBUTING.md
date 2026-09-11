# Contributing

Development is branch- and pull-request-driven.

## Required checks

Before opening or updating a pull request:

```bash
make ci
```

Changes must keep the repository public-safe: no credentials, private endpoints, customer data, internal infrastructure details or unrelated materials.

## Database migration immutability

A migration becomes immutable byte-for-byte as soon as it is included in any official published Control Center release.

- Never modify an already released `*.up.sql` file, including whitespace, formatting, comments or semantically equivalent rewrites.
- Every new database schema or data change must be implemented as a new migration with a new monotonically ordered migration identifier.
- Historical migration checksums are part of the release compatibility contract. CI and release qualification must fail closed on any checksum drift in an already released migration.
- Do not repair checksum drift by rewriting `schema_migrations` in an installed database. Restore the canonical released migration bytes or issue a new forward migration/release instead.
- Any violation of this rule is a release blocker because it can make supported upgrades fail before new migrations are applied.

## Branches

- `main` — integration baseline;
- `release/*` — release lines;
- `develop/core` — core/API/state work;
- `develop/market` — Market and provider work;
- `develop/automation` — automation and orchestration;
- `develop/ui` — product UI;
- focused feature branches are encouraged for isolated changes.
