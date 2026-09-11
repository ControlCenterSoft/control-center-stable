# Contributing

Development is branch- and pull-request-driven.

## Required checks

Before opening or updating a pull request:

```bash
make ci
```

Changes must keep the repository public-safe: no credentials, private endpoints, customer data, internal infrastructure details or unrelated materials.

## Branches

- `main` — integration baseline;
- `release/*` — release lines;
- `develop/core` — core/API/state work;
- `develop/market` — Market and provider work;
- `develop/automation` — automation and orchestration;
- `develop/ui` — product UI;
- focused feature branches are encouraged for isolated changes.
