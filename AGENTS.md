# Notes for coding agents

[CONTRIBUTING.md](CONTRIBUTING.md) is the full development guide. This file is
the short list of what agents most often get wrong; each item links to where
the detail lives.

## Before you say a change is done

```bash
make lint && make test
```

Both, actually run. Neither needs network access — hermetic tests point the CLI
at an `httptest.Server` via `MYBOX_API_BASE`
([CONTRIBUTING.md](CONTRIBUTING.md#hermetic-tests)).

## Hard rules

- **Never commit or print a token.** Nothing starting `mbx_pat_`, no `.env`, no
  config file, not in tool output either.
- **The e2e suite hits a real MYBOX account — ask before running it.** Stay
  inside the scratch folder (`/mybox-cli-e2e`) and clean up
  ([CONTRIBUTING.md](CONTRIBUTING.md#testing-against-a-real-account)).
- **Do not deliberately trip a rate limit.** MYBOX restricts accounts it reads
  as abusive, without warning.
- **Conventional Commits, no `Co-Authored-By:` trailer**
  ([CONTRIBUTING.md](CONTRIBUTING.md#commits)).

## Things that look like bugs but are not

Verified API behaviour, each written up in
[docs/api-reference.md](docs/api-reference.md) — check there before "fixing":

- **The upload part name must be exactly `Filedata`**, and **uploads are POST +
  multipart with a hand-computed `Content-Length`** — chunked bodies are
  rejected, so do not switch the envelope to an `io.Pipe`
  ([Uploading](docs/api-reference.md#uploading)).
- **No `Authorization` header goes to the storage host**; the URL's `stoken`
  authenticates ([Uploading](docs/api-reference.md#uploading)).
- **`Content-Range` has no `bytes ` prefix**, and **resume works only with
  `modifiedTime` spelled in KST and `isOverwrite` left unset** — both handled
  in `internal/cli/up.go`, do not "simplify" either
  ([Resuming](docs/api-reference.md#resuming)).
- **A trashed resource still answers by id**, **purging is eventually
  consistent**, and **there is no path-to-id API** — `internal/resolve` walks
  the tree because that is the only way
  ([Gaps](docs/api-reference.md#gaps-that-shape-the-implementation)).

## When you touch tests

No `t.Context()` in cleanup functions, and take API fixtures from Naver's
documented examples — both explained in
[CONTRIBUTING.md](CONTRIBUTING.md#testing-against-a-real-account).

## When you touch prose

[README.md](README.md) and [README.ko.md](README.ko.md) mirror each other:
update both, or neither. Everything else is English; the Korean file and folder
names in test data stay ([CONTRIBUTING.md](CONTRIBUTING.md#prose)).
