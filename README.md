# gh-image

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

## Requirements

- A supported browser with an active GitHub session
- Write access to the target repository

## Limitations

- This tool uses an undocumented GitHub internal API that could change without notice.
- The `uploadToken` required for uploads is only available to users with write access to the target repository.
- Either `--repo` must be provided or the tool must be run from within a git workspace with a GitHub remote.
