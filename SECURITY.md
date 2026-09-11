# Security policy

## Repository rules

- Never commit credentials, private keys, tokens, populated environment files or customer data.
- Never commit private infrastructure topology or environment-specific access details.
- Authentication and authorization are enforced server-side.
- External requests must not become arbitrary shell execution.
- Privileged operations must use typed, allowlisted actions with explicit authorization and audit evidence.
- Runtime images run as a non-root user.

## Reporting

Do not disclose a suspected vulnerability in a public issue. Use the repository owner's private security-reporting channel when available.
