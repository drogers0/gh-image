<p align="center">
  <img src="https://github.com/user-attachments/assets/d3455b90-f94f-4013-a00a-ebaff090635e" alt="gh-image banner" width="640">
</p>

<p align="center">
  <em>Drop images and files into GitHub issues, PRs, and READMEs, straight from the command line.</em>
</p>

<p align="center">
  <a href="https://github.com/drogers0/gh-image/releases/latest"><img src="https://img.shields.io/github/v/release/drogers0/gh-image?color=blue" alt="Latest release"></a>
  <a href="https://github.com/drogers0/gh-image/stargazers"><img src="https://img.shields.io/github/stars/drogers0/gh-image?style=flat&color=yellow" alt="GitHub stars"></a>
  <a href="https://github.com/drogers0/gh-image/releases"><img src="https://img.shields.io/github/downloads/drogers0/gh-image/total?color=green" alt="Total downloads"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/drogers0/gh-image?color=lightgrey" alt="License: MIT"></a>
  <a href="https://github.com/drogers0/gh-image/actions/workflows/lint.yml"><img src="https://github.com/drogers0/gh-image/actions/workflows/lint.yml/badge.svg" alt="Lint"></a>
  <a href="https://skills.sh/drogers0/gh-image"><img src="https://skills.sh/b/drogers0/gh-image" alt="skills.sh"></a>
</p>

---

GitHub has no public API for the attachment uploads its web UI accepts via drag-and-drop. That internal endpoint produces `user-attachments` URLs whose visibility is scoped to the repository they were uploaded to. `gh-image` replicates that flow as a `gh` CLI extension, so you can drop a screenshot — or any GitHub-supported file like a PDF, zip, or log — into a bug report, README, or Slack thread without leaving the terminal, and uploads on private repos stay private. Images render as inline embeds, videos as inline players, and other files as download links.

```console
$ gh image screenshot.png
![screenshot.png](https://github.com/user-attachments/assets/88f4599a-…-bc24)

$ gh image report.pdf
[report.pdf](https://github.com/user-attachments/files/123456/report.pdf)
```

## Installation

```bash
gh extension install drogers0/gh-image
```

That's it. The [`gh` CLI](https://cli.github.com) auto-detects your platform and downloads the prebuilt binary. Pre-built releases ship for **macOS** (arm64, amd64), **Linux** (amd64, arm64), **Windows** (amd64), and **Android/Termux** (arm64).

<details>
<summary>Build from source</summary>

```bash
git clone https://github.com/drogers0/gh-image
cd gh-image
go build -o gh-image
gh extension install .
```

Requires Go 1.26+.

</details>

## Usage

```bash
# Upload an image (infers repo from the current git workspace)
gh image screenshot.png

# Upload multiple files at once (images or anything GitHub accepts)
gh image hero.png diagram.png chart.png

# Upload any GitHub-supported file (PDF, zip, log, …) — renders as a download link
gh image report.pdf

# Target a specific repository
gh image screenshot.png --repo owner/repo
```

Each successful upload prints a ready-to-paste reference on its own line — an inline embed for images, a bare URL for videos (which GitHub renders as an inline player), and a download link for other files:

```
![hero.png](https://github.com/user-attachments/assets/…)
https://github.com/user-attachments/assets/…
[report.pdf](https://github.com/user-attachments/files/…/report.pdf)
```

If any upload fails, the error is printed to stderr and the process exits non-zero — other files in the batch still upload.

### Pipe directly into an issue, PR, or comment

From inside the repo's working directory, both `gh image` and `gh issue create` infer the target repository automatically:

```bash
gh issue create \
  --title "Login button stuck in loading state" \
  --body "Repro on staging:

$(gh image bug.png)

Happens consistently after the third click."
```

## Use with AI agents

`gh-image` is packaged as an [agent skill](https://agentskills.io), so AI coding agents can upload and embed images or attach files for you — just ask in natural language, e.g. *"attach this screenshot to the PR"* or *"file an issue and attach this log."*

```bash
npx skills add drogers0/gh-image
```

The open [Agent Skills standard](https://agentskills.io/clients) is supported by **Claude Code**, **OpenAI Codex**, **Cursor**, **GitHub Copilot**, and [many more](https://agentskills.io/clients). The skill walks the agent through installing this extension (if needed), running the upload, and embedding the resulting `user-attachments` URL into a PR, issue, or comment.

## Authentication

`gh-image` authenticates with your existing GitHub session — **no tokens to provision, no OAuth scopes to configure** for everyday local use. The tool reads the `user_session` cookie from your browser's encrypted cookie store.

**Supported browsers:** Chrome · Brave · Chromium · Edge · Firefox · Opera · Safari

**Supported platforms:** macOS · Linux · Windows · Android (Termux)

On macOS, a Keychain prompt may appear on first use to authorize access to your browser's cookie encryption key. Click **Always Allow** to skip future prompts.

> [!NOTE]
> **When browser cookies aren't available:** Chrome 127+ on Windows isn't yet supported by the underlying cookie library ([workarounds](https://github.com/drogers0/gh-image/issues/4)), and Android (Termux) has no browser cookie store at all. In either case, supply the token explicitly via `GH_SESSION_TOKEN` (see [Session token override](#session-token-override) below); on Windows you can also just use another browser.

### Session token override

For CI, headless environments, or shared machines, you can supply the session token explicitly. Resolution order (first match wins):

| Priority | Source | When to use |
|---|---|---|
| 1 | `--token <value>` flag | One-off invocations |
| 2 | `GH_SESSION_TOKEN` env var | CI/CD, shared machines, non-standard browsers |
| 3 | Browser cookie store | Local interactive use (default) |

```bash
# Flag (visible in process listings like `ps aux` — avoid on shared machines)
gh image --token "$MY_TOKEN" screenshot.png --repo owner/repo

# Environment variable (preferred — not visible to `ps aux`)
GH_SESSION_TOKEN="$MY_TOKEN" gh image screenshot.png --repo owner/repo

# Non-standard browser not auto-detected (Firefox forks like Floorp/LibreWolf)?
GH_SESSION_TOKEN="$(sqlite3 ~/path/to/profile/cookies.sqlite "SELECT value FROM moz_cookies WHERE name='user_session' AND host LIKE '%github.com'")" gh image screenshot.png --repo owner/repo
```

> [!WARNING]
> `user_session` cookies grant **full account access** — they are not scoped like personal access tokens. Treat them with the same care as a password. If leaked, **[sign out of GitHub](https://github.com/logout)** on the machine that holds the session; if you are not on that machine, revoke it through [Settings → Sessions](https://github.com/settings/sessions), or [change your password](https://github.com/settings/security) (which kills every session in one action).


## CI / CD

`gh-image` runs unattended in GitHub Actions when given a session token via `GH_SESSION_TOKEN`.

> [!CAUTION]
> **Use a dedicated bot account for CI/CD on shared repos.** GitHub hides secret values in the UI and masks log emissions, but a determined collaborator with write access can craft a workflow that exfiltrates the value through channels masking doesn't cover. Storing your *personal* `user_session` means such a leak compromises your account; a bot account scopes the blast radius to that bot. Decide whose token to extract in step 1 below accordingly.

**Setup**

1. Run `gh image extract-token` locally to capture the token (token → stdout, status → stderr), then run `gh image check-token --token <token>` to confirm it authenticates as the intended user (username → stdout on success, exit code `0` = valid).
2. Create a GitHub environment (Settings → Environments → New environment), e.g. `gh-image`, and restrict deployment branches to a trusted set (e.g. `main` only).
3. Add the token as an **environment secret** named `GH_SESSION_TOKEN` on that environment.

```yaml
jobs:
  upload:
    runs-on: ubuntu-latest
    environment: gh-image                                # binds this job to the scoped environment
    steps:
      - name: Upload screenshots
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}              # for gh CLI auth
          GH_SESSION_TOKEN: ${{ secrets.GH_SESSION_TOKEN }}  # for the upload itself
        run: |
          gh extension install drogers0/gh-image
          gh image check-token                                # optional: fail fast if the session expired
          gh image screenshot.png --repo ${{ github.repository }}
```

> [!NOTE]
> `user_session` cookies expire when GitHub invalidates the session. A scheduled `check-token` job is the cleanest way to detect expiry before it breaks a real run.

## How it works

1. Resolves a `user_session` cookie from the configured source (flag → env → browser).
2. Fetches the target repository's page to obtain an `uploadToken` from the embedded JS payload.
3. Requests an S3 upload policy from `/upload/policies/assets`.
4. Uploads the file directly to S3 using the presigned form fields.
5. Calls back to GitHub to finalize the asset, using the finalize endpoint GitHub returns in the policy (`/upload/assets/{id}` for images, `/upload/repository-files/{id}` for other files).
6. Prints the reference to stdout: `![name](url)` for images, the bare URL for videos (GitHub renders it as an inline player), or `[name](url)` for other files.

The final URL is `https://github.com/user-attachments/assets/<uuid>` for images and `https://github.com/user-attachments/files/<id>/<name>` for other files — visibility inherits from the target repository, so a private-repo upload requires authentication to view.

For the full architecture, see **[documentation/architecture.md](documentation/architecture.md)**. For the reverse-engineered upload protocol, see **[documentation/github-image-upload-flow.md](documentation/github-image-upload-flow.md)**.

## Requirements

- A supported browser with an active GitHub session — or a `GH_SESSION_TOKEN` for CI.
- Write access to the target repository (uploads require it).
- A target repository — pass `--repo owner/repo`, or run from a git workspace whose `origin` remote is on GitHub.
- The `gh` CLI must be installed and authenticated (used for repository ID lookup).

## Limitations

- Uses an **undocumented** internal GitHub API that may change without notice.
- `uploadToken` is only issued to users with write access on the target repository.
- Session cookies are not scoped credentials; they expire when GitHub invalidates the session.

## Contributing

Issues and pull requests are welcome. For bug reports, please include:

- Your OS and browser
- The exact `gh image` invocation
- The error output (with any session token values redacted)

Before opening a PR, run `go test ./...` and `go vet ./...`.

## Support

If `gh-image` saves you a few drag-and-drops, a ⭐ helps others find it:

```bash
gh api --method PUT user/starred/drogers0/gh-image
```

(or just click the star at the [top of this page](https://github.com/drogers0/gh-image))

## License

[MIT](LICENSE) © 2025-2026 drogers0
