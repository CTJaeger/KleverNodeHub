# Security Policy

## Reporting a Vulnerability

We take the security of Klever Node Hub seriously. If you discover a security vulnerability, please report it through GitHub's built-in private vulnerability reporting.

**Do not open a public issue for security vulnerabilities.**

### How to Report

1. Go to the [Security tab](https://github.com/CTJaeger/KleverNodeHub/security) of this repository
2. Click **"Report a vulnerability"**
3. Fill out the form with as much detail as possible

### What to Include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### What to Expect

- **Acknowledgment** within 48 hours
- **Status update** within 7 days
- We will work with you to understand and address the issue before any public disclosure

## Supported Versions

| Version | Supported |
|---|---|
| Latest release | Yes |
| Older releases | No |

## Security Design

Klever Node Hub follows [Kerckhoffs's principle](https://en.wikipedia.org/wiki/Kerckhoffs%27s_principle) — security relies on keys, not code secrecy. Key security features include:

- **mTLS** between Dashboard and Agents (Ed25519 certificates)
- **AES-256-GCM** encryption at rest for sensitive configuration
- **Argon2id** password hashing
- **Command whitelist** on agents (no arbitrary shell access)
- **Rate limiting** on authentication endpoints

For more details, see the [README](README.md#security).
