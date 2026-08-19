# mybox --json output shapes and recipes

Field names below are exactly what the CLI emits. Timestamps are RFC 3339
strings. Listings are top-level JSON arrays.

## Output shapes

### `ls` — array of resource objects

`resourceId`, `name`, `parentId`, `type` (`"file"` or `"folder"`), `size`,
`category` (files only), `createdAt`, `modifiedAt`, `accessedAt`,
`lastModifiedBy`, `isFavorite`, `isHidden`, and on folders `fileCount` and
`subFolderCount`.

### `stat` — one resource object

Same fields as an `ls` item.

### `search files` / `search folders` — array of results

`resourceId`, `name`, `parentId`, `parentPath`, `path`, `createdAt`,
`modifiedAt`, plus `category` and `size` on files.

Search results are the only server responses that carry a full `path` —
useful when you have an id and need to learn where it lives.

### `trash ls` — array of trashed resource objects

Like `ls` items plus `deletedAt`; `parentId` points at the trash container,
and there is no path.

### `df` — one object

`usedBytes`, `quotaBytes`, `maxFileBytes`, `trashAutoDeleteDays`,
`fileCounts` (`archive`, `audio`, `document`, `etc`, `executable`, `image`,
`video`, `total`).

### `auth status` — one object

`valid`, `profile`, `tokenSource`, `token` (redacted), `quotaBytes`,
`usedBytes`, `rateLimits` (`default`, `search`, `delete`, `restore` — the
budgets in effect, per minute).

### `auth list` — one object

`path` (the config file) and `profiles`.

### `mkdir` — one object per created folder

`path` and `resourceId`.

### Everything else

`cp`, `mv`, `rename`, `star`/`unstar`, `trash restore` and
`trash autodelete` emit small result objects echoing the affected resource.
`rm`, `trash rm`, `trash empty`, `up` and `down` emit no JSON — read the exit
code.

## Recipes

### Resolve a path once, then work by id

```bash
id=$(mybox --json stat /문서/2026/report.pdf | jq -r .resourceId)
mybox down "id:$id" -o ./
mybox mv "id:$id" /보관
```

One resolution walk, then every later step is a single API call.

### Bounded listings

`-n/--limit` stops after N entries (`0` = all): `mybox --json ls /사진 -n 50`.
Prefer a bound when you only need a sample — listing pages also spend the
call budget.

### Safe deletion

```bash
mybox --json ls /임시            # show the user what is there
# ...user confirms the deletion...
mybox rm -y /임시/draft1.txt /임시/draft2.txt
```

List first, get the user's confirmation, then delete with `-y` in one
command — multi-target commands resolve everything before acting.

### Restore from the trash

```bash
mybox --json trash ls | jq -r '.[] | "\(.resourceId)\t\(.name)\t\(.deletedAt)"'
mybox trash restore id:hV3sQ9pLzR2m
```

Restoring by name stops on ambiguity rather than guessing, so prefer the id.
`--overwrite` replaces a same-named entry at the restore location.
