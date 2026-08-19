---
name: mybox
description: Operates Naver MYBOX cloud storage through the mybox CLI — list, search, upload, download, organize, and trash files. Use when asked to work with files in Naver MYBOX (네이버 마이박스), or when a task mentions mybox paths, MYBOX quotas, or the mybox command.
license: MIT
---

# Driving the mybox CLI

`mybox` is a command-line client for Naver MYBOX. This skill covers what
`--help` cannot teach: credentials, machine-readable output, the path/ID cost
model, rate-limit etiquette, and the sharp edges. For flags and syntax,
`mybox <command> --help` is authoritative and costs no API calls — prefer it
over guessing.

Before writing jq filters or planning a multi-step file operation, read
[reference.md](reference.md) in this skill's directory: it lists the `--json`
output fields per command, plus recipes for common flows.

## Preflight

Run `mybox auth status` first. Exit code 3 means no working credentials.

- Credentials come from `--token`, `$MYBOX_TOKEN`, or a stored profile
  (`~/.config/mybox/config.json`; select with `--profile` or `$MYBOX_PROFILE`).
- `mybox auth login` is interactive — the token is typed and not echoed. Do
  not try to drive it. Ask the user to run it themselves, or to export
  `MYBOX_TOKEN=mbx_pat_...`.
- Never echo, log, or commit a token. Anything starting `mbx_pat_` is a
  secret.

## Machine-readable output

Always pass `--json` when you will parse the result. stdout carries results
only; incidental messages go to stderr, so pipes stay clean.

```bash
mybox --json ls /문서 | jq -r '.[] | select(.type=="file") | .resourceId'
```

`up` and `down` have no JSON mode: they report progress on stderr, and the
exit code is the result.

## Paths are expensive, IDs are free

There is no path-lookup API. Resolving `/a/b/c` costs one listing call per
segment (cached locally for 24 hours per account). So:

- Harvest `resourceId` values from `--json` output and address things as
  `id:<resourceId>` — that skips resolution entirely.
- After files are moved or renamed outside this CLI (web UI, phone app), run
  `mybox cache clear`, or pass `--no-cache` once.
- Trash entries have no path at all. Address them with the `id:` shown by
  `mybox --json trash ls`.

## Branch on exit codes, not on message text

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General failure |
| 2 | Usage error |
| 3 | Authentication failed (401, 403) — on a previously working setup, usually an expired token (they last 180 days at most) |
| 4 | Not found (404) |
| 5 | Rate limited (429) |
| 6 | Out of storage (507) |
| 130 | Interrupted |

## Rate-limit etiquette

Budgets are per minute and small — 60 for most calls, as low as 10 for search
— and MYBOX may restrict an account whose bursts read as abuse.

- One listing answers most questions; never loop per-file `stat` calls over a
  folder you could `ls` once.
- 429s are already retried with backoff, honouring `Retry-After`. Do not add
  a retry loop on top; a persistent exit 5 means slow down.
- Do not raise `--rate` unless the user has said which plan they are on.

## Destructive commands

`rm`, `trash rm`, and `trash empty` prompt for confirmation, and without a
TTY they refuse rather than proceed — so agent use needs `-y/--yes`. That
flag is consent on the user's behalf:

- Pass `-y` only when the user explicitly asked for the deletion — never to
  make an error go away.
- `rm` only moves to the trash (recoverable). `trash rm` and `trash empty`
  are permanent.
- `trash empty` also destroys items the user deleted from the web UI — items
  you have never seen. Always confirm with the user before running it.
- `trash autodelete <days>` changes an account-wide setting; same rule.

## Sharp edges

- `--resume` and `--overwrite` cannot be combined; given both, `--overwrite`
  is ignored.
- A trashed file still answers `stat id:...`. To learn whether something is
  gone, ask by path or check a listing — not by id.
- `down` refuses to clobber an existing local file without `--overwrite`.
- Commands taking several targets resolve all of them before acting on any:
  a typo in the last argument aborts the whole command, nothing partial.
- Password-protected folders and folders shared *with* the user are invisible
  to this API entirely — that is a service limit, not a usage error.
- Downloads have a daily budget (500–50,000 per day depending on plan).

## Command map

- Reading: `df` (quota and counts), `ls [path]`, `stat path`,
  `search files [query]`, `search folders [query]`
- Transfer: `up local... [folder]`, `down path...` (`-o -` streams to stdout)
- Organize (within MYBOX): `mkdir [-p] path...`, `cp src dst`, `mv src dst`,
  `rename path new-name`, `star`/`unstar path...`
- Trash: `trash ls`, `trash restore target...`, `trash rm target...`,
  `trash empty`, `trash autodelete [days]`
- Housekeeping: `auth login|status|logout|list`, `cache info|clear`,
  `version`
