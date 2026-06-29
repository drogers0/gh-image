# GitHub Internal Image Upload Flow

## Overview

GitHub does not provide a public API for uploading attachments (images or files like PDFs and zips) to issues/PRs. The web UI uses an internal 3-step flow involving GitHub's servers and S3. This document describes exactly how that flow works, reverse-engineered from HAR captures. The flow is identical for images and other files; only the finalize path and the resulting URL/markdown differ (noted in Step 3 and Final Result).

Attachments uploaded this way are scoped to the repository's visibility — private repo uploads require authentication to view (unlike GitHub Release assets, which are always public on public repos).

## Prerequisites

The only browser credential needed is the `user_session` cookie from `github.com`. GitHub also requires the `__Host-user_session_same_site` cookie for CSRF validation on the upload endpoints — this cookie has the same value as `user_session` (just with a stricter SameSite policy), so it can be synthesized from `user_session` rather than read separately. Everything else (CSRF tokens, S3 presigned URLs) is derived during the flow.

## The Flow

### Step 0: Obtain the `uploadToken`

**Request:** `GET https://github.com/{owner}/{repo}`

Fetch the repository's main page while authenticated (with the `user_session` cookie). The HTML response contains an embedded JavaScript payload with an `uploadToken` field:

```
"uploadToken":"<base64-encoded-token>"
```

Extract it with:
```
re.search(r'"uploadToken":"([^"]+)"', page_html)
```

This token serves as the `authenticity_token` for the upload policy request (Step 1). Standard `authenticity_token` values from HTML forms do **not** work for the upload endpoint — only this specific `uploadToken` is accepted.

**Note:** The `uploadToken` is found on the repository page, not on the PR/issue page.

### Step 1: Request Upload Policy

**Request:** `POST https://github.com/upload/policies/assets`

**Content-Type:** `multipart/form-data`

**Required Headers:**
| Header | Value |
|---|---|
| `accept` | `application/json` |
| `origin` | `https://github.com` |
| `referer` | `https://github.com/{owner}/{repo}` |
| `x-requested-with` | `XMLHttpRequest` |

**Required Cookies:** `user_session` (the `_gh_sess` session cookie is also sent automatically if using a session that fetched the repo page)

**Form Fields:**
| Field | Description |
|---|---|
| `name` | Original filename (e.g., `screenshot.png`) |
| `size` | File size in bytes (must match exactly) |
| `content_type` | MIME type (e.g., `image/png`) |
| `authenticity_token` | The `uploadToken` from Step 0 |
| `repository_id` | Numeric repository ID |

**How to get `repository_id`:** Use the GitHub API: `gh api repos/{owner}/{repo} --jq .id`

**Response:** `201 Created` with JSON body:

```json
{
  "upload_url": "https://github-production-user-asset-<id>.s3.amazonaws.com",
  "header": {},
  "asset": {
    "id": 123456789,
    "name": "screenshot.png",
    "size": 34735,
    "content_type": "image/png",
    "href": "https://github.com/user-attachments/assets/<uuid>",
    "original_name": "screenshot.png"
  },
  "form": {
    "key": "<user-id>/<asset-id>-<uuid>.png",
    "acl": "private",
    "policy": "<base64-encoded-s3-policy>",
    "X-Amz-Algorithm": "AWS4-HMAC-SHA256",
    "X-Amz-Credential": "<access-key-id>/<date>/us-east-1/s3/aws4_request",
    "X-Amz-Date": "<date>T000000Z",
    "X-Amz-Signature": "<hex-signature>",
    "Content-Type": "image/png",
    "Cache-Control": "max-age=2592000",
    "x-amz-meta-Surrogate-Control": "max-age=31557600"
  },
  "same_origin": false,
  "asset_upload_url": "/upload/assets/123456789",
  "upload_authenticity_token": "<token-for-s3-upload>",
  "asset_upload_authenticity_token": "<token-for-finalize-step>"
}
```

**Key fields in response:**
- `asset.href` — The final URL where the attachment will be served
- `asset.id` — Used in the finalize step
- `form` — All fields needed for the S3 upload (presigned)
- `upload_url` — The S3 endpoint to POST to
- `asset_upload_authenticity_token` — **The CSRF token required for Step 3 (finalize)**. This is different from the token used in Step 1.

### Step 2: Upload File to S3

**Request:** `POST {upload_url}` (the S3 URL from Step 1 response)

**Content-Type:** `multipart/form-data`

**Headers:** No GitHub authentication needed. The presigned policy handles S3 authorization.
| Header | Value |
|---|---|
| `origin` | `https://github.com` |

**Form Fields:** Send all key-value pairs from the `form` object in the Step 1 response, in order. Then append the file as the **last** field:

| Field | Value |
|---|---|
| `key` | From `form.key` |
| `acl` | From `form.acl` (`"private"`) |
| `policy` | From `form.policy` (base64 encoded) |
| `X-Amz-Algorithm` | From `form` |
| `X-Amz-Credential` | From `form` |
| `X-Amz-Date` | From `form` |
| `X-Amz-Signature` | From `form` |
| `Content-Type` | From `form` (e.g., `image/png`) |
| `Cache-Control` | From `form` |
| `x-amz-meta-Surrogate-Control` | From `form` |
| `file` | The actual file binary (**must be last**) |

**Important:** Do not add extra `Content-Type`, `Cache-Control`, or `x-amz-meta-Surrogate-Control` fields manually — they are already included in the `form` object from Step 1. Duplicating them causes S3 to reject the upload with `403 AccessDenied: Invalid according to Policy`.

**Response:** `204 No Content` on success.

### Step 3: Finalize the Upload

**Request:** `PUT https://github.com{asset_upload_url}`

Where `asset_upload_url` is taken verbatim from the Step 1 response. GitHub routes the
finalize to a different path per file type: `/upload/assets/{id}` for images and
`/upload/repository-files/{id}` for other files (PDF, zip, …). Use the server-provided
path rather than hardcoding one.

**Content-Type:** `multipart/form-data`

**Required Headers:** Same as Step 1 (`accept`, `origin`, `referer`, `x-requested-with`)

**Required Cookies:** `user_session` (same session as Step 1)

**Form Fields:**
| Field | Value |
|---|---|
| `authenticity_token` | The `asset_upload_authenticity_token` from Step 1's response |

**This step is required.** Without it, the `asset.href` URL returns 404. The finalize call tells GitHub the S3 upload completed and the asset should be marked as ready/servable.

**Response:** `200 OK` with JSON:

```json
{
  "id": 123456789,
  "name": "screenshot.png",
  "size": 34735,
  "content_type": "image/png",
  "href": "https://github.com/user-attachments/assets/<uuid>",
  "original_name": null
}
```

## Final Result

The `href` value is the permanent attachment URL. Its shape depends on the file type:
```
https://github.com/user-attachments/assets/{uuid}        # images
https://github.com/user-attachments/files/{id}/{name}    # other files (PDF, zip, …)
```

It can be referenced in any GitHub markdown (PR descriptions, issue bodies, comments). Images
embed inline; other files render as a download link:
```markdown
![alt text](https://github.com/user-attachments/assets/{uuid})
[report.pdf](https://github.com/user-attachments/files/{id}/report.pdf)
```

## Authentication Summary

| Step | Auth Required |
|---|---|
| 0 (repo page) | `user_session` + `__Host-user_session_same_site` cookies |
| 1 (upload policy) | `user_session` + `__Host-user_session_same_site` cookies + `uploadToken` as `authenticity_token` |
| 2 (S3 upload) | None (presigned URL) |
| 3 (finalize) | `user_session` + `__Host-user_session_same_site` cookies + `asset_upload_authenticity_token` from Step 1 |

`__Host-user_session_same_site` has the same value as `user_session` — it exists as a stricter SameSite=Strict duplicate for CSRF protection. Both must be present or GitHub returns 422.

The `_gh_sess` cookie rotates with each request but is managed automatically when using an HTTP session (e.g., `requests.Session` in Python, `http.Client` with `cookiejar` in Go).

## Token Relationships

```
GET /repo page
 └─> uploadToken (embedded in page JS)
      │
      ▼
POST /upload/policies/assets  (authenticity_token = uploadToken)
 └─> asset_upload_authenticity_token (in JSON response)
      │
      ▼
PUT {asset_upload_url}  (authenticity_token = asset_upload_authenticity_token)
```

Each step produces the token needed for the next GitHub-authenticated step. The S3 upload (Step 2) uses a self-contained presigned policy and needs no GitHub tokens.

## Caveats

- This is an undocumented internal API. It could change without notice.
- The `uploadToken` is only present on repository pages when the authenticated user has write access.
- The `repository_id` must correspond to a repo the user has access to.
- The presigned S3 policy has an expiration window (observed ~30 minutes).
- File size in the policy request must match the actual file size exactly.
