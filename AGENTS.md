# Notes for coding agents

Read [CONTRIBUTING.md](CONTRIBUTING.md) first — it is the full guide, and
everything below is a pointer to the parts most easily got wrong.

## Before you say a change is done

```bash
make lint && make test
```

Both, actually run. Neither needs network access.

## Hard rules

- **Never commit a token.** Nothing starting `mbx_pat_`, no `.env`, no config
  file. Do not print one in tool output either.
- **The e2e suite hits a real MYBOX account.** Ask before running it. It creates,
  uploads and deletes real files. Work only inside the scratch folder
  (`/mybox-cli-e2e`) and clean up afterwards.
- **Do not deliberately trip a rate limit.** MYBOX restricts accounts it reads as
  abusive, without warning.
- **Commits use Conventional Commits and carry no `Co-Authored-By:` trailer.**

## Things that look like bugs but are not

The API has behaviour Naver does not document, recorded in
[docs/api-reference.md](docs/api-reference.md). Check there before "fixing" any
of it:

- **The upload part name must be exactly `Filedata`.** Any other casing is a 400.
- **Uploads are POST + multipart only.** PUT is not routed and answers 404, and
  a chunked body is a 400. That is why `Content-Length` is computed by hand —
  switching the multipart envelope to an `io.Pipe` makes Go send chunked and
  breaks every upload.
- **`Content-Range` has no `bytes ` prefix.** It is not the RFC 9110 spelling.
- **`resume` only works when `modifiedTime` is spelled in KST and `isOverwrite`
  is left unset.** Any other timezone restarts the upload from zero with no
  error at all. Both are handled in `internal/cli/up.go`; do not "simplify"
  either.
- **No `Authorization` header goes to the storage host.** The URL authenticates
  itself through its `stoken`.
- **A trashed resource is still readable by id;** only its `parentId` changes.
  Read deletion from a listing, not from a single-resource fetch.
- **Purging is eventually consistent** — the id answers for a moment, then 404s.
- **There is no path-to-id API.** `internal/resolve` walks the tree from the root
  because that is the only way, not because something better was missed.

## When you touch tests

- **Do not use `t.Context()` in a cleanup function.** Go cancels it just before
  cleanups run, so every command issued from one fails silently. Use the
  harness's `e.ctx`; `TestE2EHarnessContextOutlivesTheTestBody` guards this.
- **Take API fixtures from Naver's documented response examples.** Do not invent
  them.
- A test that changes an account-wide setting must be opt-in and must write the
  original value to disk before changing it.

## When you touch prose

[README.md](README.md) and [README.ko.md](README.ko.md) mirror each other. Update
both, or neither. Everything else is English only.

Everything in the source is English. The Korean that remains is test data —
realistic MYBOX file and folder names — and it stays: it is what keeps UTF-8
handling covered.
