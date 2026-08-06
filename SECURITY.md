# Security Policy

## What this tool handles

`gh-image` reads your GitHub `user_session` cookie from your browser's encrypted cookie store (or from an explicit token source) and uses it to authenticate against GitHub's internal image upload API.

The cookie grants **full account access** — equivalent to your GitHub password, and not scoped like a personal access token. See the [Authentication](README.md#authentication) section of the README for full details on how the cookie is sourced and used.

## Why the credential can't be scoped

The upload endpoint accepts a `user_session` cookie and nothing else. A PAT, an OAuth app token, and a GitHub App installation token all return 404 — this is a GitHub platform limitation, not a design choice here, and GitHub's own agent tooling has filed against it:

- [cli/cli#9046](https://github.com/cli/cli/issues/9046) — no `gh` CLI support for attachment upload
- [github/gh-aw#21242](https://github.com/github/gh-aw/issues/21242) — GitHub's agentic-workflows team hitting the same wall
- [community#162417](https://github.com/orgs/community/discussions/162417), [community#54551](https://github.com/orgs/community/discussions/54551) — no documented API, tokens rejected

If GitHub ships a scoped credential for this endpoint, `gh-image` will adopt it.

## Automated scan findings

The [agent skill](skills/github-image-upload/SKILL.md) is scanned by third-party tools. Findings we have addressed, and findings we accept, are listed here so the reasoning is on the record rather than implied by a badge.

### Addressed

| Finding | Resolution |
|---|---|
| `REMOTE_CODE_EXECUTION`, `EXTERNAL_DOWNLOADS`, Snyk `W012` | The skill no longer installs the extension. It detects, and directs the user to install. Release binaries carry [build provenance attestations](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations), verifiable with `gh attestation verify <file> --owner drogers0`. |
| `COMMAND_EXECUTION` | The skill declares an `allowed-tools` allowlist naming only the `gh` subcommands it needs. `gh extension install` is deliberately excluded. Enforcement is host-dependent — hosts that ignore `allowed-tools` get no restriction from it. |
| `PROMPT_INJECTION` (partial) | PR and issue bodies are untrusted. The documented workflow keeps them in a shell variable and pipes them, so body text never enters the agent's context; where a body must be read, the skill requires boundary markers around it. |

### Accepted

| Finding | Why it stands |
|---|---|
| `DATA_EXFILTRATION` | Uploading local files to GitHub is what the tool does. It is mitigated — the skill confirms the destination repo before uploading and stops rather than guessing an ambiguous path — but not removable without removing the tool. |
| `CREDENTIALS_UNSAFE` | GitHub supports exactly one credential here (above). Reading the browser cookie store stays the local default because the alternative pushes an unscoped, password-equivalent token into shell history and dotfiles — worse placement than the OS keychain it came from. `GH_SESSION_TOKEN` remains available for anyone who prefers to manage it explicitly. |
| `PROMPT_INJECTION` (residual) | The read-modify-write of a PR body uses stock `gh` commands by design. Moving that logic into `gh-image` would eliminate the round-trip, at the cost of making this a tool that mutates GitHub objects rather than one that prints a URL. Not a trade we are making. |

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
