# Security policy

## Supported versions

Version `0.3.1` receives security fixes. Older versions are unsupported.

## Reporting a vulnerability

Do not open a public issue containing exploit details, credentials, private
addresses, or customer data. Contact the repository owner privately with the
affected version, reproducible impact, and a minimal proof of concept.

## Operational requirements

- Keep the environment file root-owned with mode `0600`.
- Never commit credentials, tokens, certificates, database dumps, or customer
  data.
- Run the supplied service as its non-root user.
- Terminate TLS before the API and restrict ingress to authorized operators.
- Keep PostgreSQL on a private network and require authenticated connections.
- Back up PostgreSQL before migrations or rollback.
- Rotate a secret immediately if it appears in logs or source.
- Rebuild promptly when the Go toolchain, PostgreSQL driver, password hashing
  library, or container base receives a security update.

Control Center rejects provider output that does not satisfy the bounded
Actual State, Health, and audit-evidence contract.
