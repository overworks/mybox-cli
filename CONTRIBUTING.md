# Contributing to mybox-cli

This file is the single source of truth for how the project is built and what is
expected of a change; [AGENTS.md](AGENTS.md) and [CLAUDE.md](CLAUDE.md) point
here rather than repeating it.

## Getting set up

```bash
git clone https://github.com/overworks/mybox-cli.git
cd mybox-cli
make build
```

Go 1.26 or newer. There are two dependencies:
[cobra](https://github.com/spf13/cobra) for subcommands and shell completion,
and `golang.org/x/term` for reading a token without echoing it.

**Add a third only with a good reason.** Table output is `text/tabwriter` and
JSON is `encoding/json`, both from the standard library. This ships as a single
static binary, so every dependency is supply-chain surface a user cannot audit.

## The two checks

Every change has to pass both. CI runs the same ones.

```bash
make lint   # gofmt + go vet
make test   # go test -race -cover, no network
```

`make test` runs to completion without an account. Every HTTP interaction in it
goes to an `httptest.Server`.

## Testing against a real account

The unit and integration suites are hermetic. Tests that talk to a live account
are behind the `e2e` build tag and excluded from `make test`:

```bash
MYBOX_TOKEN=mbx_pat_... go test -tags e2e -v ./internal/cli/
```

Rules for anything that touches a live account:

- **Work only inside the scratch folder** (`/mybox-cli-e2e`). Never read, move or
  delete a resource outside it.
- **Clean up.** Report a failed cleanup with `t.Errorf` and print the command to
  run by hand. Never let it pass quietly with `t.Logf` — a run that leaves the
  account changed has not passed.
- **Do not use `t.Context()` in a cleanup function.** Go cancels it just before
  cleanups run, so every command issued from one fails instantly and silently.
  The harness provides `e.ctx` for this, and
  `TestE2EHarnessContextOutlivesTheTestBody` guards the invariant.
- **A test that changes an account-wide setting is opt-in.** Such a setting
  cannot be scoped to the scratch folder, and MYBOX offers no way to read back
  what a value used to be, so a failed restore is unrecoverable. Require
  `MYBOX_E2E_ALLOW_SETTING_CHANGES=1` and write the original value to disk
  **before** changing it.
- **Never commit a token**, paste one into a commit message, or echo one in
  output a tool prints.
- **Do not deliberately exhaust a rate limit.** MYBOX states that bursts it reads
  as abuse can restrict an account without prior warning, and search allows as
  few as 10 calls a minute.

`trash empty` is deliberately not exercised: it would destroy items the account
owner deleted from the web, not just ours. It stays a manual check.

## Layout

```
cmd/mybox/          entry point: signal handling and exit codes
internal/
  api/              HTTP client; the only place that speaks to the MYBOX API
    client.go       request execution, retries, rate limiting
    limiter.go      per-group token buckets
    errors.go       the shared error envelope
    types.go        response models
    drive.go trash.go search.go   the twenty endpoints
  resolve/          path-to-id resolution and its per-account cache
  transfer/         uploads and downloads against the storage host
  config/           profiles and token storage
  output/           table and JSON rendering
  cli/              cobra command definitions
docs/               the API reference, including what Naver does not document
```

## Conventions

- **Everything is English**, including the validation messages in `internal/api`
  that reach the user unchanged. The only Korean left in the source is test
  data — file and folder names like `회의록.pdf` — which is deliberate: MYBOX is
  a Korean service, and those fixtures keep the UTF-8 paths honest.
- **Comments explain *why*.** Which endpoint a method calls is already in its
  signature. What belongs in prose is the constraint that is not obvious — a
  quota, an ordering guarantee, a field MYBOX omits, why a value has to be
  spelled a particular way.
- **Validate before the call where you can.** Per-minute budgets are tight
  enough that rejecting an impossible request locally beats a round trip. A
  malformed global flag is caught in `PersistentPreRunE` so it is not masked by
  whatever the command needed first.
- **Incidental messages go to stderr.** stdout carries results only, so `--json`
  output can be piped straight into `jq`.
- **Destructive actions confirm.** Without a TTY there is nobody to ask, so they
  refuse rather than proceed; `--yes` is the way through.
- **A command taking several targets resolves them all before acting on any.** A
  typo in the third argument must not surface after the first two are gone.
- **Mind the call budget.** Resolving a path costs one listing per level. Put
  what you learn in the cache, and invalidate it after a mutation.

## Adding an endpoint

1. Add the method to the matching file in `internal/api`, taking an option struct
   if it has more than a few parameters.
2. Add the response model to `types.go`.
3. Add a unit test asserting the request path, method and body, and the
   deserialised result. **Take the fixture from Naver's documented response
   example** rather than inventing one.
4. If it is a listing, expose an `Iter…` variant built on `iterPages`.
5. Add the command in `internal/cli` and a row to the tables in
   [README.md](README.md) **and** [README.ko.md](README.ko.md).
6. If it needs a new rate-limit group, add it in `limiter.go`. `api.GroupByName`
   is the only definition of those names — do not repeat the strings elsewhere.

## Undocumented behaviour

Naver's documentation stops short of the storage transfer calls and says nothing
about several behaviours the CLI has to handle. What has been established against
the live service lives in [docs/api-reference.md](docs/api-reference.md).

If you find something new, record it there **as observed**, and say plainly which
parts are verified and which are inferred. A confident guess in that file is
worse than an admitted gap.

## Prose

[README.md](README.md) and [README.ko.md](README.ko.md) mirror each other. Update
both, or neither. Everything else — this file, `AGENTS.md`, `docs/` — is English
only.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`,
`docs:`, `refactor:`, `test:`, `chore:`.

Write the body for someone reading `git log` in a year: what changed and why, not
a restatement of the diff. **Do not add `Co-Authored-By:` trailers.**

Branch off `0.x`; open a pull request rather than pushing to it directly.

```
fix: stop using a cancelled context in e2e cleanups

Go cancels t.Context() just before cleanup functions run, so every cleanup
command failed instantly and left both the scratch folder and a changed
account setting behind. Commands now run on a context that outlives the
test body's own cleanups.
```
