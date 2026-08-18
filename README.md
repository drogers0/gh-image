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
<summary>Verify build provenance</summary>

Release binaries are signed with [build provenance attestations](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations), which prove a binary was built by this repository's release workflow from a specific commit. To check one before installing, download it from the [releases page](https://github.com/drogers0/gh-image/releases) and run:

```bash
gh attestation verify darwin-arm64 --owner drogers0
```

Note that `gh extension install` does not verify attestations itself — this is an explicit check for anyone who wants it.

</details>

<details>
<summary>Build from source</summary>

```bash
git clone https://github.com/drogers0/gh-image
cd gh-image
go build -o gh-image
gh extension install .
```

Requires Go 1.26+.

The cookie backend defaults to [`kooky`](https://github.com/browserutils/kooky); `go build -tags hbd` uses [`hackbrowserdata`](https://github.com/moonD4rk/HackBrowserData) instead. The `hbd` build pins a hackbrowserdata commit carrying the macOS securityd-dump gate and library log silencing until its next release; override either dep with `go get <module>@<ref>` or a `go.mod` `replace`.

</details>

<details>
<summary>HackBrowserData cookie backend (prebuilt)</summary>

An alternate build swaps the browser-cookie reader from `kooky` to [`hackbrowserdata`](https://github.com/moonD4rk/HackBrowserData) for broader browser coverage (Arc, Vivaldi, Firefox WAL, …). It ships as separate `hbd-<os>-<arch>` release assets — `gh extension install` still gets the default `kooky` build. Download one and run it directly:

```bash
gh release download <tag> --repo drogers0/gh-image \
  --pattern hbd-darwin-arm64 --output gh-image --clobber   # pick your platform
chmod +x gh-image
./gh-image screenshot.png
```

| Platform | `--pattern` |
| --- | --- |
| macOS Apple Silicon | `hbd-darwin-arm64` |
| macOS Intel | `hbd-darwin-amd64` |
| Linux x86-64 | `hbd-linux-amd64` |
| Linux arm64 | `hbd-linux-arm64` |
| Windows x64 | `hbd-windows-amd64.exe` |

Pre-releases need the explicit tag (`gh` won't treat them as latest). Verify provenance with `gh attestation verify gh-image --owner drogers0`, or build it yourself with `-tags hbd` (see *Build from source*).

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

### Download

```bash
# Fetch attachments, named from the URL, into the current directory
gh image download <url>... [--output-dir <dir>] [--no-clobber]

# Or send a single attachment somewhere specific — `-` for stdout
gh image download <url> --output <file>
```

Existing files are overwritten unless `--no-clobber` is passed, which writes `name.1`, `name.2` instead. As with upload, a failed URL is reported to stderr and the process exits non-zero — the rest of the batch still downloads.

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

The installer detects your local agents automatically. To install the skill globally for several harnesses at once, name them explicitly:

```bash
npx skills add drogers0/gh-image --skill github-image-upload \
  --agent claude-code codex opencode grok --global
```

`bunx skills add` is an equivalent option for Bun users. The open [Agent Skills standard](https://agentskills.io/clients) is supported by **Claude Code**, **OpenAI Codex**, **OpenCode**, **Grok**, **Cursor**, **GitHub Copilot**, and [many more](https://agentskills.io/clients). The skill checks that this extension is present (asking you to install it if not), runs the upload, and embeds the resulting `user-attachments` URL into a PR, issue, or comment. It never installs anything on your behalf.

## Who's using gh-image

Shipping visual evidence in production review pipelines:

<table width="100%">
<tr>
<td valign="top">

<a href="https://github.com/NousResearch/hermes-agent"><img src="https://avatars.githubusercontent.com/u/134168893?s=48&v=4" width="18" align="top"></a> &nbsp;<b><a href="https://github.com/NousResearch/hermes-agent/blob/main/.github/workflows/publish-e2e-evidence.yml">NousResearch/hermes-agent</a></b> &nbsp;<code>&#9733; 226k+</code>
<br><br>
Automated <b>visual proof on every single pull request</b>, with no human in the loop: the <a href="https://github.com/NousResearch/hermes-agent/blob/main/.github/workflows/publish-e2e-evidence.yml">publish job</a> uploads the E2E screenshots inline using a specially scoped attachment bot account.

</td>
</tr>
</table>

<table width="100%">
<tr>
<td width="50%" valign="top">

<a href="https://github.com/openshift/console"><img src="https://avatars.githubusercontent.com/u/792337?s=48&v=4" width="18" align="top"></a> &nbsp;<b><a href="https://github.com/openshift/console/blob/main/.claude/skills/qa-verify/scripts/upload-evidence.sh">openshift/console</a></b><br>
<code>&#9733; 450+</code> &nbsp;&middot;&nbsp; Red Hat
<br><br>
Its <code>/qa-verify</code> agent skill hands reviewers <b>full-resolution QA evidence</b> &mdash; CDN-hosted, so nothing gets downscaled to fit a comment.

</td>
<td width="50%" valign="top">

<a href="https://github.com/Simpleyyt/ai-manus"><img src="https://avatars.githubusercontent.com/u/2818827?s=48&v=4" width="18" align="top"></a> &nbsp;<b><a href="https://github.com/Simpleyyt/ai-manus/blob/main/.cursor/skills/demo-videos/SKILL.md">Simpleyyt/ai-manus</a></b><br>
<code>&#9733; 1.6k+</code>
<br><br>
A <code>demo-videos</code> skill publishes the <b>README demo reels</b> &mdash; <code>gh image</code> is mandatory, since only <code>user-attachments</code> URLs autoplay inline.

</td>
</tr>
</table>

## Authentication

`gh-image` authenticates with credentials you already have — **nothing to provision, no OAuth scopes to configure**. Images and video going to a repository you can push to are uploaded with your `gh` CLI token; everything else — other file types, and repositories you cannot push to — falls back to your existing GitHub session, read as the `user_session` cookie from your browser's encrypted cookie store. Downloads take the same two routes: the `gh` token first, your browser session as fallback.

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
GH_SESSION_TOKEN="$(sqlite3 ~/path/to/profile/cookies.sqlite "SELECT value FROM moz_cookies WHERE name='user_session' AND host LIKE '%github.com'")" \
  gh image screenshot.png --repo owner/repo
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

1. Tries a single authenticated upload with the `gh` CLI token; on failure falls back to the browser-session flow below, resolving a `user_session` cookie from the configured source (flag → env → browser).
2. Fetches the target repository's page to obtain an `uploadToken` from the embedded JS payload.
3. Requests an S3 upload policy from `/upload/policies/assets`.
4. Uploads the file directly to S3 using the presigned form fields.
5. Calls back to GitHub to finalize the asset, using the finalize endpoint GitHub returns in the policy (`/upload/assets/{id}` for images, `/upload/repository-files/{id}` for other files).
6. Prints the reference to stdout: `![name](url)` for images, the bare URL for videos (GitHub renders it as an inline player), or `[name](url)` for other files.

The final URL is `https://github.com/user-attachments/assets/<uuid>` for images and `https://github.com/user-attachments/files/<id>/<name>` for other files. Until it is referenced in rendered content it resolves only for the uploader, even on a public repo; once referenced, visibility follows the content that references it.

For the full architecture, see **[documentation/architecture.md](documentation/architecture.md)**. For the reverse-engineered upload protocol, see **[documentation/github-image-upload-flow.md](documentation/github-image-upload-flow.md)**.

## Requirements

- A supported browser with an active GitHub session — or a `GH_SESSION_TOKEN` for CI.
- Read access to the target repository — write access is not required.
- A target repository — pass `--repo owner/repo`, or run from a git workspace whose `origin` remote is on GitHub.
- The `gh` CLI must be installed and authenticated (used for repository ID lookup).

## Limitations

- Uses an **undocumented** internal GitHub API that may change without notice.
- `uploadToken` is usually present on repository pages for any user who can view the repo. An invalid or expired session is the case where it is absent.
- Session cookies are not scoped credentials; they expire when GitHub invalidates the session.
- Uploads are attributed to the account that authenticated them, so if your browser session and your `gh` login are different accounts, images and video may be attributed differently from other files. Supply a session token explicitly to pin every upload to one account.

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
