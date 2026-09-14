# Themis-CLI
a CLI/TUI Client for the RUG judging software (aka Themis)

## Build

```sh
go build -o themis ./cmd/themis
```

## Usage

### themis
Open the interactive TUI application.

```sh
./themis \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json"
```

If the persisted session is invalid, bare `themis` opens the login TUI first and then continues into the app on success.

### login
Authenticate either interactively or non-interactively.

Interactive login:

```sh
./themis login \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json"
```

Non-interactive login:

```sh
./themis login \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json" \
  --username "s1234567" \
  --password "secret" \
  --totp "123456"
```

Notes:
- `themis login` with no auth flags opens the login TUI.
- `themis login` with any of `--username`, `--password`, or `--totp` performs a non-interactive login attempt.
- If username/password are already saved in auth settings, `--totp` alone is enough for non-interactive login.
- Non-TUI commands never open the login TUI; they return `not authenticated` or `session expired`.

### auth
View or update persisted auth settings stored alongside the session.

```sh
./themis auth \
  --session-file "$HOME/.config/themis/session.json"
```

Example updates:

```sh
./themis auth --save-username=true --save-password=true
./themis auth --username "s1234567"
./themis auth --clear-password
```

### check
Validate base URL access and authentication.

```sh
./themis check \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json"
```

### list test cases
Probe tests from a tests URL (or a specific test file URL) and return valid indices (`N.in` and `N.out` must both exist).

```sh
./themis list \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json" \
  --tests-url "https://themis.housing.rug.nl/file/.../%40tests/1.in" \
  --start 1 --max 50 --max-misses 5
```

### list assignments recursively
Discover assignment/exercise URLs so agents can find targets without manually provided exercise URLs.

```sh
./themis list \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json" \
  --discover \
  --root-url "https://themis.housing.rug.nl/course/2025-2026/os/" \
  --discover-depth 6
```

### fetch
Download discovered `.in/.out` pairs.

```sh
./themis fetch \
  --base-url "https://themis.housing.rug.nl" \
  --session-file "$HOME/.config/themis/session.json" \
  --tests-url "https://themis.housing.rug.nl/file/.../%40tests/1.in"
```

Default output directory is `./tests` for the `fetch` command (resolved to absolute path at runtime).  
If missing, it is created automatically. Override with `--out`.

### project link
Link the current repository to a Themis course root so state-first discovery and TUI can resolve the active root without `--root-url`.

```sh
./themis project link \
  --base-url "https://themis.housing.rug.nl" \
  --root-url "https://themis.housing.rug.nl/course/2025-2026/os" \
  --default-refresh-depth 1
```

This writes `.themis/project.json` in the repo root.

Refresh a linked project and update local state:

```sh
./themis project refresh --depth 8
```

This prints progress as each Themis page is fetched. Bare `./themis` also detects a linked project root that is missing from local state and offers to refresh it before opening the TUI.

TUI behavior:
- Opens cached state immediately, then refreshes the displayed course in the background (at least two levels, or the configured deeper refresh depth). Catalog/year roots refresh only their own page to avoid a broad startup crawl. Use `--auto-refresh-on-open=false` to keep startup cache-only.
- Uses compact task icons (`✓` passed, `×` needs attention, `·` unsubmitted/ready) and grouped details with deadlines, progress bars, and file previews. `i` shows or hides technical source details.
- Animates activity during refresh/download/preview loading and briefly acknowledges completion; animation timers stop when idle. Use `--reduced-motion` or `THEMIS_REDUCED_MOTION=1` for static indicators.
- Shows a course summary computed from leaf assignments only, including submitted percentage, passed count, overdue warnings, and the nearest deadline group. Parent assignments complete automatically when all their subtasks pass; they are not counted as additional tasks.
- Supports targeted refresh actions from the selected node.
- Keeps panels within the terminal bounds and scrolls to keep the selection visible. Below 72 columns, shows one panel at a time; `Tab` switches between tree and details. Below 24 columns or 8 rows, shows a resize hint.
- `j/k` or arrow keys move through the focused panel; `PgUp/PgDn` page and `g/G` or `Home/End` jump to the first/last item. `?` opens scrollable keyboard help.
- `Esc`, `h`, or `d` closes the file panel. Active downloads continue in the background; `d` reopens their progress. Refreshes wait until downloads finish.
- Download mode shows a file picker beside a line-numbered content preview (below 80 columns, `Tab` switches between them). `j/k`, `PgUp/PgDn`, and `g/G` scroll the focused pane; `r` retries the preview. Previews use [Chroma](https://github.com/alecthomas/chroma) for source-code tokenization with terminal-palette highlighting. Test inputs/outputs and unknown extensions stay plain text.
- Previews fetch asynchronously after a brief navigation pause, cancel on file changes, and cache up to eight files until the picker closes. Reads are limited to 64 KiB, 500 lines, and 4,096 characters per line; truncated previews are labeled. Other binary/non-UTF-8 files show an explanatory message. Previewing does not write files.
- PDF previews extract text with page separators and wrap to the pane width. They use Poppler's `pdftotext` from `PATH` (`brew install poppler` on macOS, or the `poppler-utils` package on Linux). PDF reads are capped at 8 MiB and extraction at 5 seconds; previews show at most 20 pages / 500 lines. Password-protected, damaged, or image-only PDFs show an explanatory message; OCR and graphical page rendering are not included.
- Chroma v2.15 is pinned to preserve this project's Go 1.21 compatibility; later Chroma v2 releases require Go 1.22.
- The file picker uses font-independent type icons: language marks such as `C`, `C+`, and `Py`, `⇥` for test input, `⇤` for expected output, and distinct document/image/archive icons. The preview identifies the selected type.
- Download mode supports multi-select and per-file progress:
  - `…` active
  - `✓` completed
  - `✗` failed
  - `·` pending
- Uses your terminal’s ANSI palette and default foreground/background colors. Selection uses a pointer and bold text without background bars. Labels and hints retain the default foreground for contrast; muted color is reserved for decoration. Theme and system appearance changes apply without restarting the TUI.

Download path rules in TUI:
- `%40tests` assets download under `./tests/...`
- Regular files download under current working directory
- Path-like regular assets (for example `/imgs/1.img`) preserve relative path (`./imgs/1.img`)

## Flags And Env

Common flags on all subcommands:
- `--base-url` or `THEMIS_BASE_URL`
- `--session-file` or `THEMIS_SESSION_FILE`
- `--json`

`list` flags:
- `--tests-url`
- `--start`
- `--max`
- `--max-misses`
- `--discover`
- `--root-url`
- `--discover-depth`
- `--refresh-url`
- `--refresh-depth`
- `--full-refresh`
- `--from-state-only`

`fetch` flags:
- `--tests-url`
- `--out` (default: `./tests`)
- `--target-dir` (deprecated alias for `--out`)

`login` flags:
- `--username`
- `--password`
- `--totp`

`auth` flags:
- `--username`
- `--password`
- `--save-username`
- `--save-password`
- `--clear-username`
- `--clear-password`

`project link` flags:
- `--root-url`
- `--default-refresh-depth`
- `--auto-refresh-on-open`
- `--show-stale-warning-after-minutes`

`project refresh` flags:
- `--depth`

Bare `themis` flags:
- `--root-url`
- `--auto-refresh-on-open` (default `true`)
- `--reduced-motion` (default `false`, or `THEMIS_REDUCED_MOTION=1`)

Auth/session persistence:
1. Session cookies, saved credentials, and auth preferences live in one session file.
2. Default session file path is `$HOME/.config/themis/session.json`.
3. Commands other than bare `themis` do not auto-launch interactive login.

## JSON Output

With `--json`, each command emits exactly one JSON object on stdout with fields:

- `status` (`"ok"` or `"error"`)
- `base_url`
- `tests`
- `downloaded`
- `files`
- `error` (only on failure)

Additional fields may be present depending on command:
- `authenticated`, `user` (`check`)
- `tests_base_url` (`list`, `fetch`)
- `assignments` (`list --discover`)
- `target_dir` (`fetch`)
- `mode`, `root_url`, `refreshed`, `refresh_scope` (`list --discover`)

Logs and human-readable output are written to stderr/non-JSON mode; JSON mode keeps stdout machine-parseable.
