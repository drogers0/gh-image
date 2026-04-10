# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`gh-image` is a Go CLI distributed as a `gh` CLI extension (`gh image <file>`). It uploads images to GitHub using the same undocumented internal API the web UI uses for drag-and-drop, producing `https://github.com/user-attachments/assets/<uuid>` URLs scoped to the target repo's visibility.

Module: `github.com/drogers0/gh-image` (Go 1.26.1). Single external dependency: `github.com/browserutils/kooky` for browser cookie extraction.

## Commands

```bash
go build ./...                      # Build (produces ./gh-image)
go run . <image-path>               # Run without building
go run . --repo owner/repo <path>   # Run against an explicit repo
go vet ./...                        # Static analysis
go test ./...                       # Test (no tests exist yet)

# Install locally as a gh extension (from this directory):
gh extension install .

# Release: push a v* tag, GitHub Actions runs GoReleaser
git tag v0.1.0 && git push --tags
```

GoReleaser (`.goreleaser.yml`) cross-compiles to darwin/linux/windows (amd64 + arm64, except windows/arm64). Binary archive names are `{os}-{arch}` — `gh extension install` expects this exact naming to auto-detect the platform.

## Architecture

The tool implements a **3-step upload protocol** reverse-engineered from HAR captures. Full protocol spec lives at `documentation/github-image-upload-flow.md` — read it before modifying anything in `internal/upload/`, since several non-obvious constraints (token chain, S3 field ordering, cookie pairing) come from that protocol.

### Package layout

- `main.go` — CLI entrypoint. **Manual arg parser** (not `flag` package) so `--repo` can appear before or after positional args. Supports `--` to separate flags from filenames starting with `-`. Uploads are attempted for each file independently; exits 1 if any fail.
- `internal/cookies/` — Reads the `user_session` cookie from Chrome/Brave/Edge/Chromium via kooky. Only imports the specific browser subpackages needed (keeps binary small).
- `internal/repo/` — Resolves `owner/name/ID`. Parses `git remote get-url origin` (SSH + HTTPS regex) when `--repo` is omitted, then calls `gh api repos/{owner}/{repo} --jq .id` for the numeric ID.
- `internal/upload/` — The 3-step flow:
  - `token.go` — `GetUploadToken`: regex-extracts `"uploadToken":"..."` from the authenticated repo page HTML.
  - `upload.go` — `Upload` orchestrator + `requestPolicy` (step 1) + `finalizeUpload` (step 3). Also `NewClient` which builds the cookie jar.
  - `s3.go` — `uploadToS3` (step 2). Presigned POST with no GitHub auth.

### Token chain (critical, easy to break)

Three distinct tokens flow through the upload, each produced by the previous step:

```
uploadToken (from repo HTML)
    → used as authenticity_token in POST /upload/policies/assets
        → response yields asset_upload_authenticity_token
            → used as authenticity_token in PUT /upload/assets/{id}
```

Standard HTML form CSRF tokens are rejected by `/upload/policies/assets` — only the `uploadToken` embedded in the repo page works. The finalize step rejects the original `uploadToken` — only `asset_upload_authenticity_token` works there.

### Dual auth model

- **Upload API (steps 0/1/3)**: browser cookies. Needs both `user_session` (from kooky) **and** `__Host-user_session_same_site` — the second is a SameSite=Strict duplicate with the **same value**, so `NewClient` synthesizes it from `user_session` rather than reading it separately. GitHub returns 422 if either is missing.
- **Repo ID lookup**: `gh` CLI (OAuth). Independent from cookies.
- **S3 upload (step 2)**: no auth — the presigned policy is self-contained.

The `_gh_sess` cookie rotates per response; the `http.Client` cookie jar handles it automatically. Don't try to read it from the browser.

### S3 upload gotchas (`s3.go`)

- The `form` fields from the policy response must be sent **exactly as-is**. Do **not** add an extra `Content-Type`, `Cache-Control`, or `x-amz-meta-Surrogate-Control` — they're already in `form` and duplicates trigger `403 AccessDenied: Invalid according to Policy`.
- The file field must be the **last** multipart field.
- Go map iteration is nondeterministic and S3 presigned uploads can be sensitive to field order — `s3.go` iterates a fixed `s3FieldOrder` slice first, then sweeps any unknown fields (future-proofing).

### Authorization constraints

- `uploadToken` is only present on the repo page when the authenticated user has **write access**. No write access → Step 0 returns a page without the token, and `GetUploadToken` fails with "uploadToken not found".
- File size in the step 1 policy request **must match the actual file size exactly**.
- The presigned S3 policy expires in ~30 minutes. There's no token caching today; each upload re-runs step 0.

## Platform notes

kooky transparently handles browser cookie decryption per platform (macOS Keychain, Linux GNOME Keyring/kwallet, Windows DPAPI). macOS may show a Keychain prompt on first use — clicking "Always Allow" persists the grant.
