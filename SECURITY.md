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
| `REMOTE_CODE_EXECUTION`, `EXTERNAL_DOWNLOADS`, Snyk `W012` | **Primary control:** the skill no longer installs the extension — it detects presence and a version floor, then directs the user to install. **Additional:** release binaries carry [build provenance attestations](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations), verifiable with `gh attestation verify <file> --owner drogers0`. Verification is opt-in and requires downloading the binary first; `gh extension install` does not check attestations. |
| `COMMAND_EXECUTION` | The skill declares an `allowed-tools` allowlist naming only the `gh` subcommands it needs. `gh extension install` is deliberately excluded. Enforcement is host-dependent — hosts that ignore `allowed-tools` get no restriction from it. |
| `PROMPT_INJECTION` (partial) | PR and issue bodies are untrusted. Every documented command keeps a body inside the shell pipeline, and the verify step counts URL matches instead of printing the body, so no documented step returns body text to the agent. The skill instructs against decomposing those pipelines, and requires boundary markers if a body is read anyway. |

### Accepted

| Finding | Why it stands |
|---|---|
| `DATA_EXFILTRATION` | Uploading local files to GitHub is what the tool does. It is mitigated — the skill confirms the destination repo before uploading and stops rather than guessing an ambiguous path — but not removable without removing the tool. |
| `CREDENTIALS_UNSAFE` | GitHub supports exactly one credential here (above), and it is unscoped. What is left is where it lives. Browser cookie store (local default) leaves it in the OS keychain it already occupies — no new copy. `GH_SESSION_TOKEN` (recommended for CI and shared machines) keeps it out of `ps aux` and shell history. The `--token` flag is visible in `ps aux`; the skill tells agents not to use it. Both defaults are reasonable placements, but the credential's blast radius is GitHub's to fix, not ours. |
| `PROMPT_INJECTION` (residual) | Appending to a PR *description* requires reading the existing body. The documented command keeps it in the shell pipeline, but anyone who decomposes that command — or reads a body for any other reason — puts attacker-controlled text in front of the agent. Eliminating the round-trip would mean moving read-modify-write into `gh-image`, making it a tool that mutates GitHub objects rather than one that prints a URL. Not a trade we are making. The comment path, which never reads a body, is documented as the preferred default. |

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
