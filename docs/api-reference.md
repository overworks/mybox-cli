# MYBOX Open API reference

Source: <https://developers.mybox.naver.com/>, collected 2026-08-19.
Naver publishes no OpenAPI or Swagger document, so this was assembled from the
documentation pages. It is what `internal/api` is implemented against and what
the test fixtures are taken from.

- **Base URL**: `https://open-api.mybox.naver.com/v1`
- **Authentication**: `Authorization: Bearer mbx_pat_xxxxxxxx`
  - Issued from MYBOX web > 설정 > 계정 및 개인 액세스 토큰 관리
  - Five per account, valid for 30/60/90/180 days, shown once at creation
- **Scope**: password-protected folders and folders shared with the account are
  not reachable through the Open API at all.
- **Account state**: an over-quota or suspended account fails every call. A
  dormant account is reactivated by one.

## The error envelope

Every endpoint returns the same body on a non-2xx status.

```json
{
  "code": "PLAT-400",
  "message": "BAD_REQUEST",
  "requestId": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "timestamp": "2026-06-18T16:30:00+09:00"
}
```

| HTTP | code | message |
|---|---|---|
| 400 | PLAT-400 | BAD_REQUEST |
| 401 | PLAT-401 | UNAUTHORIZED |
| 403 | PLAT-403 | FORBIDDEN |
| 404 | PLAT-404 | NOT_FOUND |
| 409 | PLAT-409 | CONFLICT |
| 422 | PLAT-422 | UNPROCESSABLE_ENTITY |
| 423 | PLAT-423 | LOCKED |
| 429 | PLAT-429 | TOO_MANY_REQUESTS |
| 500 | PLAT-500 | INTERNAL_SERVER_ERROR |
| 502 | PLAT-502 | BAD_GATEWAY |
| 503 | PLAT-503 | SERVICE_UNAVAILABLE |
| 507 | PLAT-507 | INSUFFICIENT_STORAGE |

Responses also carry an `x-request-id` header, which may differ from the
`requestId` in the body.

## Rate limits by plan

| API | 30GB | 80GB | 180GB–330GB | 2TB | 5TB | 10TB | 20TB |
|---|---|---|---|---|---|---|---|
| Download | 500/day | 1,000/day | 1,000/day | 2,000/day | 5,000/day | 20,000/day | 50,000/day |
| Search | 10/min | 10/min | 30/min | 30/min | 30/min | 30/min | 30/min |
| Delete | 60/min | 60/min | 240/min per API | ← | ← | ← | ← |
| Restore | 180/min | 180/min | 240/min per API | ← | ← | ← | ← |
| Everything else | 60/min per API | ← | 240/min per API | ← | ← | ← | ← |

Daily budgets reset each day, per-API budgets each minute.

A client cannot discover which plan an account is on, so the CLI shapes calls to
the lowest documented figures and lets the user raise them.

## Shared schemas

### `openapi.resourceItem`

| Field | Type | Required | Notes |
|---|---|---|---|
| resourceId | string | ✓ | |
| name | string | ✓ | |
| parentId | string | ✓ | |
| type | string | ✓ | `file` or `folder` |
| size | integer | ✓ | |
| category | string | | File kind |
| createdAt | string | ✓ | RFC 3339, +09:00 |
| modifiedAt | string | ✓ | |
| accessedAt | string | ✓ | |
| lastModifiedBy | string | ✓ | |
| isFavorite | boolean | ✓ | |
| isHidden | boolean | ✓ | |

Fetching a folder through endpoint 5 adds `fileCount` and `subFolderCount`.

### `openapi.trashedResourceItem`

`resourceItem` without `isFavorite`/`isHidden`, plus `deletedAt`.

### `openapi.responseMetaData`

| Field | Type | Notes |
|---|---|---|
| nextCursor | string | Absent or empty on the last page |

### Sorting and categories

- `sort` takes the form `field,direction`.
  - Drive fields: `name`, `createdAt`, `modifiedAt`, `accessedAt`
  - Trash fields: `deletedAt` (default), `name`, `type`, `location`, `size`
  - Directions: `asc`, `desc`
  - **Results come back folders first, then files, whatever `sort` says.**
- `category`: `image`, `video`, `audio`, `document`, `archive`, `executable`, `etc`

---

## Endpoints

### 1. Storage properties — `GET /drive/storage`

```json
{
  "fileCounts": {"archive":5,"audio":8,"document":30,"etc":23,"executable":2,"image":40,"total":120,"video":12},
  "maxFileBytes": 53687091200,
  "quotaBytes": 32212254720,
  "trashAutoDeleteDays": 5,
  "usedBytes": 5368709120
}
```

`quotaBytes` includes capacity shared out to other users and to mail;
`usedBytes` likewise includes shared-out usage.

### 2. Trash auto-delete interval — `PATCH /drive/storage`

Body: `{"trashAutoDeleteDays": 5}` — one of `0` (off), `5`, `15`, `30`, `50`.
Answers `{"trashAutoDeleteDays": 5}`.

### 3. Root listing — `GET /drive/resources`

Query: `sort`, `count` (≤1000, default 100), `cursor`.

Answers `{fileCount, subFolderCount, resources: resourceItem[], responseMetaData}`.
The two counts cover **direct** children only; nothing nested is included.

### 4. Folder listing — `GET /drive/folders/{folderId}/resources`

Same query and response as endpoint 3. `folderId` is a `resourceId` from an
earlier listing.

### 5. Single resource — `GET /drive/resources/{resourceId}`

Answers a `resourceItem`, plus `fileCount` and `subFolderCount` for a folder.

### 6. Create folder — `POST /drive/folders`

Body: `{"folderName": "업무자료", "parentId": "..."}` — omit `parentId` for the root.
Answers `201 {"name": "...", "resourceId": "..."}`.

### 7. Reserve an upload — `POST /drive/files`

| Field | Type | Required | Notes |
|---|---|---|---|
| fileName | string | ✓ | Include the extension |
| fileSize | integer | ✓ | Bytes, ≥ 0. Omitting it is an error |
| parentId | string | | Root when omitted |
| isOverwrite | boolean | | Replace a same-named file |
| resume | boolean | | Continue an interrupted upload; send with `modifiedTime` |
| modifiedTime | string | | Required whenever `resume` is set |

Answers `201 {"offset": 0, "uploadUrl": "https://open-api-fs.mybox.naver.com/v1/storage/upload?auth=4&stoken=..."}`.

The URL points at a **different host**. See
[Storage transfer protocol](#storage-transfer-protocol) for what to send it.

### 8. Reserve a download — `GET /drive/files/{fileId}/download`

Answers `{"downloadUrl": "https://open-api-fs.mybox.naver.com/v1/storage/download?auth=3&resourceKey=...&atoken=...", "expiresIn": 600}`.

**Single use, valid for ten minutes.** Fetch it with a plain `GET` and no
`Authorization` header.

### 9. Copy — `POST /drive/resources/{resourceId}/copy`

Body, all optional: `name` (original name when omitted; include the extension to
keep it), `parentId` (root when omitted), `isOverwrite` (default false).
Answers `201 {"name": "...", "resourceId": "..."}`.

### 10. Move — `POST /drive/resources/{resourceId}/move`

Body: `parentId` (required), `isOverwrite` (optional).
Answers **200 with no body**.

### 11. Rename — `POST /drive/resources/{resourceId}/rename`

Body: `{"name": "회의록.pdf"}`. Answers `{"name": "..."}`. **The ID is unchanged.**

### 12. Delete — `DELETE /drive/resources/{resourceId}`

Moves the resource to the trash. Answers 204.

### 13–14. Favourite — `POST /drive/resources/{resourceId}/favorite` and `/unfavorite`

Answers `{"isFavorite": bool, "resourceId": "..."}`. **Both are idempotent.**

### 15. Trash listing — `GET /drive/trash`

Query: `sort` (default `deletedAt,desc`), `count` (≤1000, default 100), `cursor`.
Answers `{fileCount, subFolderCount, resources: trashedResourceItem[], responseMetaData}`.

### 16. Restore — `POST /drive/trash/{resourceId}/restore`

Body: `isOverwrite` (optional). Restores to the original location.
Answers **200 with no body**.

### 17. Purge one — `DELETE /drive/trash/{resourceId}`

Permanent. Answers 204.

### 18. Empty the trash — `DELETE /drive/trash`

Permanently deletes everything in the trash. Answers 204.

### 19. Search files — `GET /search/resources/files`

| Query | Notes |
|---|---|
| q | Spaces and extensions are ANDed |
| category | See the category list above |
| parentPath | Restrict to that folder's subtree |
| dateField | `created` (default) or `modified` |
| startDate / endDate | e.g. `2026-01-01T00:00:00+09:00` |
| count | 20–200, default 20 |
| cursor | |

**At least one of `q`, `category` or a date bound is required.**

Answers `{resources: dtos.FileResource[], responseMetaData}`, where
`dtos.FileResource` is
`{resourceId, name, parentId, parentPath, path, category, size, createdAt, modifiedAt}`
— unlike the listing endpoints, **search reports a path**.

### 20. Search folders — `GET /search/resources/folders`

Same as 19 without `category` and with `path` added. Setting `path` pins the
search to exactly that folder and **ignores every other criterion**. At least one
of `q`, `path` or a date bound is required.

`dtos.FolderResource` is
`{resourceId, name, parentId, parentPath, path, createdAt, modifiedAt}`.

---

## Storage transfer protocol

Naver documents how to *reserve* a transfer URL but not how to send bytes to it.
What follows was established against the live service on 2026-08-19. Source:
`docs/transfer-protocol.md` in
[`overworks/php-mybox`](https://github.com/overworks/php-mybox); the `Filedata`
part name was cross-checked against the
[MYBOX Sync Obsidian plugin](https://github.com/choihc/mybox-sync).

### Uploading

```http
POST /v1/storage/upload?auth=4&stoken=… HTTP/1.1
Host: open-api-fs.mybox.naver.com
Content-Type: multipart/form-data; boundary=…
Content-Length: …

--…
Content-Disposition: form-data; name="Filedata"; filename="report.pdf"
Content-Type: application/octet-stream

<raw bytes>
--…--
```

```json
200 {"resourceId": "dXJsaW5lZXwzNDcy…", "name": "report.pdf", "fileSize": 11}
```

Four details are load-bearing.

- **POST only.** `PUT`, `GET` and `HEAD` are not routed and answer 404;
  `OPTIONS` answers 200.
- **`multipart/form-data` only.** Raw octet-stream, `text/plain`, JSON,
  form-urlencoded, no Content-Type, **chunked**, `multipart/mixed`,
  `multipart/related` and multipart without a boundary are all rejected with
  `400 {"message":"Invalid Data Format"}`.
  Because chunked is refused, **`Content-Length` has to be computed and sent**.
- **The part must be named `Filedata`**, with exactly that capitalisation — the
  legacy Flash-uploader convention Naver's storage tier still follows.
  `FileData`, `fileData` and `filedata` are all rejected with
  `400 {"message":"Param Not Exist"}`.
- **No `Authorization` header.** The URL authenticates itself through `stoken`,
  and the storage host is not the API host the personal access token belongs to.

`auth` is a mode selector: `4` for upload, `3` for download. Reserved upload URLs
are **reusable** — they are not consumed on first touch.

### Resuming

- **The size must be exact.** If the bytes delivered do not match the reserved
  `fileSize`, the answer is `500 {"message":"File Storage Error"}`.
- `Content-Range` takes the bare form **`{offset}-{fileSize-1}/{fileSize}`**,
  without RFC 9110's `bytes ` prefix.
- **`modifiedTime` is matched as a literal string, and only in KST.** The same
  instant written as `2026-01-01T18:04:05+00:00` instead of
  `2026-01-02T03:04:05+09:00` is treated as a different file, and the upload
  silently restarts from zero.
- **`isOverwrite` suppresses the offset.** Reserving with it reports `offset: 0`,
  because asking to overwrite means starting the file again. The retained bytes
  are not destroyed — reserving again without it reports them — but the two
  options cannot be combined in one reservation.
- For a second or two after a connection dies, reserving again answers
  `423 LOCKED`; the offset appears once that clears.

Independently reproduced with this CLI (2026-08-19): a 12 MiB file cut at 5 MiB,
one `423 LOCKED` retry three seconds later, an offset reported at exactly the cut
point, the remaining 7 MiB sent, and the downloaded file matching by SHA-256. No
delivered byte was discarded. (`TestE2EResumeAfterInterruptedUpload`)

### Downloading

Fetch `downloadUrl` with a plain `GET` and no `Authorization` header.

---

## Gaps that shape the implementation

1. **There is no path-to-id lookup.** Endpoints 3, 4, 5 and 15 report a
   `parentId` but never a path; only search returns one. A path-based interface
   therefore has to walk the tree from the root, matching names as it goes
   (`internal/resolve`).
2. **The root folder has no ID.** It is listed only through its own endpoint (3),
   and omitting `parentId` in endpoints 6, 7 and 9 is what "the root" means.
3. **A trashed resource is still readable by id.** Deleting changes its
   `parentId` to a trash container; the id keeps answering. Whether something was
   deleted has to be read from a folder or trash listing, not from endpoint 5.
4. **Purging is eventually consistent.** For well under a second after
   `DELETE /drive/trash/{id}`, the id still answers, reporting `size: 0`, before
   it starts 404ing.
