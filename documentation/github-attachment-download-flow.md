# GitHub Attachment Download Flow

## Overview

Fetching a `user-attachments` asset is a two-leg operation. A `GET` on the
attachment URL answers with a `302` to a presigned storage URL; the bytes come
from that second URL. The legs have **opposite** credential requirements, which
is the single most important thing to know about this protocol.

Companion to [github-image-upload-flow.md](github-image-upload-flow.md).

## The two URL shapes

GitHub routes uploads to one of two shapes depending on the file type, and they
behave differently on the way back out.

| | `/user-attachments/assets/<uuid>` | `/user-attachments/files/<id>/<name>` |
|---|---|---|
| Used for | images and videos | PDF, zip, log, txt — everything else |
| Filename in the URL | **no**, only a uuid | **yes** |
| Presigned host | `github-production-user-asset-*.s3.amazonaws.com` | `objects.githubusercontent.com` |
| `response-content-disposition` on the redirect | no | yes |

Both redirect targets carry `X-Amz-Expires=300` and `response-content-type`.

## The Flow

### Step 1: Resolve

Either credential works:

```
GET https://github.com/user-attachments/assets/<uuid>
Authorization: Bearer <gh auth token>
User-Agent: …
```

```
GET https://github.com/user-attachments/assets/<uuid>
Cookie: user_session=…; __Host-user_session_same_site=…
User-Agent: …
```

The bearer token is the fast path — it needs no browser and no keychain access.
Verified against a private repository, for an asset uploaded by a different
user, so the grant follows repository read permission rather than uploader
identity. Both URL shapes work, unlike the bearer *upload* endpoint, which
accepts only a narrow set of content types.

On the cookie route both cookies are required. Sending only `user_session` looks
correct and fails to authenticate — the same requirement the browser-session
upload endpoints have.

The response is a `302`. **Do not follow it automatically**; classify it:

| Redirect target | Meaning |
|---|---|
| `/login`, resolved host exactly `github.com` | the session token is invalid or expired |
| absolute URL carrying `X-Amz-Signature` | the asset — proceed to step 2 |
| anything else | refuse; do not fetch |

Order matters. Checking for the signature first turns an expired session into a
confusing "unusable target" error. The host check on `/login` matters too:
matching by path alone would let `https://attacker.example/login` be read as
"your credential is stale", which on a tool that reads a browser cookie store is
a prompt an attacker should not be able to trigger.

The third row is what prevents an SSO interstitial or error page from flowing
into step 2, returning `200` with a consistent `Content-Length`, and landing on
disk as a perfectly plausible attachment.

### Step 2: Fetch

```
GET <presigned URL>
User-Agent: …
```

**No credentials of any kind.** The presigned URL is its own capability.

An `Authorization` header here returns **400** on the S3 bucket — AWS rejects
two auth mechanisms on one request. `objects.githubusercontent.com` tolerates
it, so the mistake is invisible on half the assets and easy to ship.

Verify the byte count against `Content-Length` when present, so a truncated
transfer never passes for a complete file.

## Filenames

Never taken from a response header:

| URL shape | Name from |
|---|---|
| `/files/<id>/<name>` | the `<name>` segment of the URL |
| `/assets/<uuid>` | the uuid plus the extension of the presigned path |

The `/files/` name is server-validated — a valid file id with a tampered
filename returns `404` — so the URL is a trustworthy source. An `/assets/` URL
carries no name at all, so the extension has to come out of the redirect, which
is why `curl -LO` on one produces an extensionless uuid.

GitHub sanitizes names at upload time: `paren (1) test.txt` is stored as
`paren.1.test.txt`.

## Authentication Summary

| Leg | Auth |
|---|---|
| Resolve (github.com) | `Authorization: Bearer` **or** `user_session` + `__Host-user_session_same_site` |
| Fetch (presigned) | none |

A `404` cannot distinguish "no such asset" from "this credential cannot read
it", so a client holding both credentials should try the second before
reporting failure.

## Observed Responses

| Condition | Result |
|---|---|
| anonymous, private asset | `404` |
| valid session, private asset | `302` to presigned |
| valid bearer token, private asset | `302` to presigned |
| invalid bearer token | `404` |
| stale session, `/assets/` | `302` to `/login` |
| stale session, `/files/` | `404` |
| well-formed but nonexistent uuid | `404` |
| nonexistent file id | `404` |
| valid file id, tampered filename | `404` |
| stray `Authorization` on the presigned leg | `400` (S3), tolerated (objects host) |
| `HEAD` on the presigned leg | `403` (S3) — signed for `GET` only |
| 30 sequential authenticated resolves | `302` every time; no `429`, no rate-limit headers |

A `404` conflates "no such asset" with "exists but you cannot read it", so error
messages have to name both.

## Caveats

- The endpoints are **undocumented** and may change without notice.
- `HEAD` is not usable for metadata on the S3 host, so asset size cannot be
  probed without fetching.
- Presigned URLs expire; treat one as valid only for the request that
  immediately follows its resolve.
- `curl -O` on an attachment URL writes **0 bytes and exits 0** — without `-L`
  it saves the empty redirect body and reports success.
