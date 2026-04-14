# gh-image

![banner](https://github.com/user-attachments/assets/92463e67-b897-4212-91b4-a4f9b80ec4d4)

A `gh` CLI extension that uploads images to GitHub from the command line.

GitHub has no public API for image uploads — the web UI uses an internal endpoint that produces URLs scoped to a repository's visibility. This tool replicates that flow, so images on private repos stay private.

## Usage

```bash
# Upload an image and get the markdown (infers repo from current git workspace)
gh image screenshot.png

# Upload multiple images
gh image img1.png img2.png

# Explicit repo (when not in a git workspace or targeting a different repo)
gh image screenshot.png --repo owner/repo
```

Output:
```
![screenshot](https://github.com/user-attachments/assets/<uuid>)
```

### Example: Upload an image and attach it to a GitHub issue

```bash
# Upload the image
gh image screenshot.png --repo owner/repo
# Output: ![screenshot.png](https://github.com/user-attachments/assets/88f4599a-...)

# Use the output in a new issue
gh issue create --repo owner/repo \
  --title "Bug report" \
  --body "Here's what I see:

![screenshot.png](https://github.com/user-attachments/assets/88f4599a-...)
"
```

## Install

```bash
gh extension install drogers0/gh-image
```

## How It Works

1. Reads your GitHub session cookie from your browser's local cookie store (encrypted cookie decryption handled automatically)
2. Uploads the image through GitHub's internal asset upload flow (the same one the web UI uses)
3. Prints a markdown image reference to stdout

The upload produces `https://github.com/user-attachments/assets/<uuid>` URLs — the same format as drag-and-drop uploads in the browser. Images inherit the repository's visibility: private repo images require authentication to view.

See [documentation/github-image-upload-flow.md](documentation/github-image-upload-flow.md) for the full reverse-engineered upload protocol.

## Authentication

No tokens or OAuth setup required. The tool reads your `user_session` cookie directly from your browser's cookie database on disk. On macOS, a Keychain prompt may appear on first use to authorize access to the browser's encryption key.

Supported browsers:
- Chrome
- Brave
- Chromium
- Edge

Supported platforms:
- macOS
- Linux
- Windows

## Session Token

The tool supports providing your own session token instead of reading from the browser. This enables use in environments where browser cookies are unavailable (such as CI).

> **Warning:** `user_session` cookies grant full account access — they are not
> scoped like personal access tokens. Treat them with the same care as a password.
> Rotate promptly if leaked.

**Token resolution order (highest priority first):**

1. `--token <value>` flag
2. `GH_SESSION_TOKEN` environment variable
3. Browser cookie extraction (default)

```bash
# Use a token via flag
gh image --token "$MY_TOKEN" screenshot.png --repo owner/repo

# Use a token via environment variable (preferred on shared machines)
GH_SESSION_TOKEN="$MY_TOKEN" gh image screenshot.png --repo owner/repo
```

> **Security note:** `--token` values are visible in process listings (`ps aux`).
> Use the `GH_SESSION_TOKEN` environment variable on shared machines.


## Token Utilities

### `extract-token`

Extracts your session token from the browser and prints it to stdout. Useful for storing your token as a CI secret.

```bash
# Extract and print token
gh image extract-token

# Store in a variable
TOKEN=$(gh image extract-token)
```

### `check-token`

Verifies that a session token is valid and, on a best-effort basis, prints the authenticated GitHub username to stdout. Exit code 0 means valid, exit code 1 means invalid or an error occurred. Username output may be empty even when the token is valid (for example, if the `<meta name="user-login">` tag is missing or cannot be parsed), so rely on the exit code for validity checks.

```bash
# Check token from flag
gh image check-token --token "$MY_TOKEN"

# Check token from environment variable
GH_SESSION_TOKEN="$MY_TOKEN" gh image check-token

# Check token from browser (default)
gh image check-token

# Capture username in a script
USERNAME=$(gh image check-token --token "$MY_TOKEN")
```

## CI/CD Usage

> **Note:** `user_session` cookies are real user login sessions, not scoped automation credentials. They expire when GitHub invalidates the session. Use a pre-push hook or periodic job to validate the token (see `check-token`) and rotate it when needed.

### Setup

1. Extract your token locally:
   ```bash
   gh image extract-token
   ```
2. Add the output as a repository secret named `GH_SESSION_TOKEN` in your GitHub repository settings.

### Example GitHub Actions workflow

```yaml
- name: Upload screenshots
  env:
    GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}         # Required for gh CLI auth
    GH_SESSION_TOKEN: ${{ secrets.GH_SESSION_TOKEN }} # GitHub session token
  run: |
    gh extension install drogers0/gh-image
    gh image --repo owner/repo screenshot.png
```

> **Note:** `gh` CLI must be authenticated (via `GH_TOKEN` or equivalent) for
> extension installation and repository ID lookup. `GH_SESSION_TOKEN` is the
> session token used for the image upload itself.

### Validate token before upload

```yaml
- name: Validate session token
  env:
    GH_SESSION_TOKEN: ${{ secrets.GH_SESSION_TOKEN }}
  run: gh image check-token

- name: Upload screenshots
  env:
    GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    GH_SESSION_TOKEN: ${{ secrets.GH_SESSION_TOKEN }}
  run: |
    gh extension install drogers0/gh-image
    gh image --repo owner/repo screenshot.png
```

### Cache extension for faster CI runs

```yaml
- name: Cache gh extensions
  uses: actions/cache@v4
  with:
    path: ~/.local/share/gh/extensions
    key: gh-extensions-${{ runner.os }}
```

## Requirements

- A supported browser with an active GitHub session (for local use)
- Write access to the target repository
- For CI: a valid `GH_SESSION_TOKEN` secret (see [CI/CD Usage](#cicd-usage))

## Limitations

- This tool uses an undocumented GitHub internal API that could change without notice.
- The `uploadToken` required for uploads is only available to users with write access to the target repository.
- Either `--repo` must be provided or the tool must be run from within a git workspace with a GitHub remote.
- Session tokens are not scoped credentials — they expire with the browser session and should be rotated periodically.
