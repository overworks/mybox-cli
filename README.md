# mybox-cli

A command-line client for Naver [MYBOX](https://mybox.naver.com). A single
static Go binary with no runtime dependencies.

*[한국어](README.ko.md)*

```console
$ mybox ls /문서/2026
1월/
2월/
회의록.pdf

$ mybox up ./report.pdf /업무자료
$ mybox search files --category document --since 2026-01-01
$ mybox df
```

## Installing

```console
$ go install github.com/overworks/mybox-cli/cmd/mybox@latest
```

Building from source needs Go 1.26 or newer.

```console
$ git clone https://github.com/overworks/mybox-cli.git
$ cd mybox-cli && make build
```

## Getting started

Create a token from MYBOX web > **설정 > 계정 및 개인 액세스 토큰 관리**. It is
shown **once**, so copy it immediately.

```console
$ mybox auth login
Personal access token: (input is not echoed)
signed in · 5.0 GiB of 30.0 GiB used (16.7%)
```

The token is written to `~/.config/mybox/config.json` with owner-only
permissions (0600). For CI, use the environment instead.

```console
$ export MYBOX_TOKEN=mbx_pat_xxxxxxxx
$ mybox df
```

## Paths and IDs

**The MYBOX API has no path lookup.** Listings report a parent ID but never a
path. So when `mybox` is given `/문서/2026` it lists the root to find `문서`,
lists that to find `2026`, and so on — one step at a time.

Each step is an API call, and most APIs allow 60 a minute, so results are cached
locally per account (24 hours by default). If you already have an ID, the `id:`
prefix skips resolution entirely.

```console
$ mybox ls /문서/2026          # resolved from the root, then cached
$ mybox stat id:hV3sQ9pLzR2m   # no resolution at all
$ mybox cache info
$ mybox cache clear            # after moving files from the web UI
```

## Commands

### Reading

| Command | What it does |
|---|---|
| `mybox df` | Quota, per-category file counts, maximum upload size |
| `mybox ls [path]` | Folder contents. `-l` detailed, `-a` include hidden, `--sort`, `-n` limit |
| `mybox stat path` | File or folder properties |
| `mybox search files [query]` | Search files. `--category`, `--in`, `--since`, `--until`, `--date-field`, `-n` limit |
| `mybox search folders [query]` | Search folders. `--path`, `--in`, `--since`, `--until`, `--date-field`, `-n` limit |

### Files

| Command | What it does |
|---|---|
| `mybox up local... [folder]` | Upload. `--overwrite`, `--resume`, `--strategy` |
| `mybox down path...` | Download. `-o dir`, `-o -` for stdout, `--overwrite` |
| `mybox mkdir [-p] path...` | Create folders |
| `mybox cp src dst` | Copy within MYBOX. `--name`, `--overwrite` |
| `mybox mv src dst` | Move, renaming on the way if asked |
| `mybox rename path name` | Rename in place; the ID is kept |
| `mybox rm path...` | Move to the trash. `-y` skips the prompt |
| `mybox star` / `unstar` | Add to or remove from favourites |

Transfers between your machine and MYBOX are `up` and `down`; `cp` copies within
MYBOX only.

### Trash

| Command | What it does |
|---|---|
| `mybox trash ls` | List. `--sort`, `-n` limit |
| `mybox trash restore target...` | Restore to the original location. `--overwrite` |
| `mybox trash rm target...` | Delete permanently. `-y` skips the prompt |
| `mybox trash empty` | Empty it. `-y` skips the prompt |
| `mybox trash autodelete [days]` | Read or set the interval (0, 5, 15, 30, 50) |

Trashed items have no path, so name them by the `id:` shown by `trash ls`, or by
name. An ambiguous name stops the command rather than picking one.

### Auth and housekeeping

| Command | What it does |
|---|---|
| `mybox auth login` | Save a personal access token. `--set-default` |
| `mybox auth status` | Verify the token and report the effective rate limits |
| `mybox auth logout` | Remove a stored token |
| `mybox auth list` | List stored profiles |
| `mybox cache info` | Where the path cache lives and how big it is |
| `mybox cache clear` | Empty the path cache |
| `mybox version` | Print the version |
| `mybox completion shell` | Completion script for bash, zsh, fish or powershell |

### Global options

| Option | What it does |
|---|---|
| `--json` | Emit results as JSON |
| `--quiet`, `-q` | Suppress incidental messages |
| `--verbose`, `-v` | Log HTTP requests (the token is masked) |
| `--token`, `--profile` | Choose credentials |
| `--no-cache` | Bypass the path cache |
| `--rate` | Override the call budgets ([below](#rate-limits)) |
| `--timeout` | Bound the whole command |

## Scripting

Incidental messages go to stderr, so stdout carries results only.

```console
$ mybox --json ls /문서 | jq -r '.[] | select(.type=="file") | .name'
$ mybox --json df | jq '.usedBytes / .quotaBytes * 100'
```

Exit codes distinguish why something failed.

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General failure |
| 2 | Usage error |
| 3 | Authentication failed (401, 403) |
| 4 | Not found (404) |
| 5 | Rate limited (429) |
| 6 | Out of storage (507) |
| 130 | Interrupted (Ctrl-C) |

## Rate limits

MYBOX allows more calls on larger plans, but **gives a client no way to discover
which plan an account is on**. Calls are therefore shaped to the lowest
documented figures.

| Group | Default (per minute) | Covers |
|---|---|---|
| `default` | 60 | Listing, properties, create, copy, move, rename, favourites, transfer URLs |
| `search` | 10 | File and folder search |
| `delete` | 60 | Trashing and permanent deletion |
| `restore` | 180 | Restoring from the trash |

Plans of 180GB and up allow 240 a minute per API (30 for search), so leaving the
defaults in place is needlessly slow. Raise them with `--rate`: a bare number is
the baseline for every group, and `group=n` overrides one.

```console
$ mybox --rate 240,search=30 ls /사진   # 180GB and up
$ mybox --rate 0 ls /사진               # no shaping; let the server say 429
```

To avoid repeating it, store it in the profile.

```json
{
  "defaultProfile": "default",
  "profiles": {
    "default": {
      "token": "mbx_pat_...",
      "limits": { "default": 240, "search": 30, "delete": 240, "restore": 240 }
    }
  }
}
```

`mybox auth status` reports what is in effect. Setting a budget higher than the
account really allows just means the server answers 429, which is retried with
backoff, honouring `Retry-After`.

## Profiles

```console
$ mybox --profile work auth login --set-default
$ mybox --profile personal auth login
$ mybox auth list
$ MYBOX_PROFILE=work mybox df
```

## Known limits

- **Password-protected folders and folders shared with you** are not supported by
  the Open API at all — only the desktop web UI and the mobile apps reach them.
- **Downloads have a daily budget** (500 to 50,000 a day, depending on plan).
- Tokens last **180 days at most** and have to be replaced before they expire.
- **A deleted file is still readable as `stat id:...`.** Trashing changes only
  its parent folder; the ID stays alive. Asking by path correctly reports that
  it is gone.
- **`--resume` and `--overwrite` cannot be combined.** Asking to overwrite tells
  MYBOX to start the file again, which reports a resume offset of zero. Given
  both, `--overwrite` is ignored.

Naver does not document the upload wire format, but it has been established
against the live service and is written up in
[docs/api-reference.md](docs/api-reference.md#storage-transfer-protocol);
`mybox` uses that format by default. It should never need attention, but if the
format changes and uploads start failing with 400 or 404, it can be measured
again.

```console
$ mybox debug upload-probe /tmp/probe.txt --dest /임시 --cleanup
$ mybox up ./file /dest --strategy post-raw
```

`--strategy` (and `MYBOX_UPLOAD_STRATEGY`) accepts `post-multipart` — the
default, and the only format the live service currently accepts — plus
`post-multipart-file`, `post-raw`, `put-raw` and `put-multipart`, kept so the
probe has something to measure with if the format ever changes.

## Environment variables

| Variable | Purpose |
|---|---|
| `MYBOX_TOKEN` | Personal access token |
| `MYBOX_PROFILE` | Profile to use |
| `MYBOX_CONFIG_HOME` | Config directory (default `~/.config/mybox`) |
| `MYBOX_CACHE_HOME` | Cache directory (default `~/.cache/mybox`) |
| `MYBOX_API_BASE` | Override the API root |
| `MYBOX_UPLOAD_STRATEGY` | Override the upload wire format ([Known limits](#known-limits)) |

## Shell completion

```console
$ mybox completion zsh > "${fpath[1]}/_mybox"
$ mybox completion bash | sudo tee /etc/bash_completion.d/mybox
$ mybox completion fish > ~/.config/fish/completions/mybox.fish
```

## Documentation

- [docs/api-reference.md](docs/api-reference.md) — the twenty endpoints, plus the
  storage transfer protocol Naver does not document and which was established by
  measurement. Naver's own pages are at <https://developers.mybox.naver.com/>.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build and test, the rules for
tests that touch a real account, and commit conventions.

## License

MIT
