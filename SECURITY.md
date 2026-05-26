# Security Policy

## What this tool handles

`gh-image` reads your GitHub `user_session` cookie from your browser's encrypted cookie store (or from an explicit token source) and uses it to authenticate against GitHub's internal image upload API.

The cookie grants **full account access** — equivalent to your GitHub password, and not scoped like a personal access token. See the [Authentication](README.md#authentication) section of the README for full details on how the cookie is sourced and used.

## Reporting a vulnerability

If you've found a security issue in `gh-image`, please report it **privately** rather than opening a public issue.

Use GitHub's [private vulnerability reporting](https://github.com/drogers0/gh-image/security/advisories/new) to submit the report.

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce
- Affected version(s)
- Any proof-of-concept code, if applicable


## If your session token has been leaked

See the warning callout in the README's [Session token override](README.md#session-token-override) section for the recommended remediation flow (sign out → revoke session → change password).

## Supported versions

Security fixes are applied to the latest release only.

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ — please upgrade |
