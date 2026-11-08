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
- Non-TUI commands never open the login TUI. With a saved username, password, and authenticator key, they renew an expired session automatically; otherwise they return an authentication error.
- `themis login --non-interactive --json` uses saved credentials without opening a prompt. An explicit `--totp` overrides the saved key.

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

#### Optional automatic 2FA

First log in and enable remembering your username and password in the login screen. Then save your authenticator's **setup key** (the base32 secret or `otpauth://totp/...` provisioning URI, not a six-digit code):

```sh
themis auth --totp-secret-stdin
```

In a terminal this prompts with hidden input. It also accepts piped input from a secret manager. The key is never accepted as a command-line argument or included in status output. Standard SHA1, six-digit, 30-second TOTP keys are supported; unsupported provisioning settings are rejected.

After configuration, CLI and TUI operations reuse valid cookies and make one automatic login attempt when the session expires. Codes are generated locally when the 2FA challenge appears. No authenticator app or interactive prompt is needed. Agent commands retain their cancellation/deadlines, and connection/server failures do not trigger a new login. An incorrect key or out-of-sync system clock causes an authentication error rather than repeated OTP attempts. Submissions are never automatically replayed.

```sh
themis auth --json                         # reports whether a key is stored
themis login --non-interactive --json      # verify or establish a session
themis auth --clear-totp-secret            # disable automatic 2FA
```

The key is opt-in and bound to the saved username and Themis instance. Changing or clearing the username clears the old key; clearing the password leaves the key but disables automatic login. Manual login preserves the key for the same account. Removing the locally saved key does not revoke the authenticator at the university.

Credentials and cookies are stored **unencrypted** in the same session file, written atomically with owner-only permissions (`0600`). This is file storage, not the macOS Keychain. A saved password and authenticator key together provide full account login access. Keep the file out of repositories, shared directories, logs, and agent context; configure the key locally rather than pasting it into chat.

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
Link the current directory to a Themis course or assignment so state-first discovery and TUI can resolve the active root without `--root-url`.

```sh
./themis project link \
  --base-url "https://themis.housing.rug.nl" \
  --root-url "https://themis.housing.rug.nl/course/2025-2026/os" \
  --default-refresh-depth 1
```

This writes `.themis/project.json` in the current directory. A subdirectory inherits the nearest parent link; a link in the subdirectory overrides it. Existing repository-root links continue to work.

Refresh a linked project and update local state:

```sh
./themis project refresh --depth 8
```

This prints progress as each Themis page is fetched. Bare `./themis` opens the linked target immediately and loads missing data in the background. Without a directory link it opens the course catalog, including on a fresh installation.

### Browse and manage directory links

- **c** opens the compact centered Catalog/Recent picker; **Tab** switches sections. Rows show titles and course codes, with the year/course context shown once per branch and extra details only for the selected item. Recents retain course/year columns to distinguish entries from different branches. Enter opens a year or course; Right browses a selected branch, Left returns to its parent, and Escape closes the picker. Unloaded branches load one page at a time; **r** retries failed requests.
- **/** opens ranked fuzzy search across downloaded titles, course codes, course acronyms, academic years, assignment paths, and grading status. For example, **PC** finds **Parallel Computing** and its exercises even if its URL code is `parcomp`. Matching ignores case, accents, punctuation, and spacing differences such as `lab1` / `Lab 1` / `lab_1`; official titles remain unchanged. Multiple words can match different metadata fields.
- Narrow searches with `course:pc`, `year:26/27`, `type:course`, `type:exercise`, `status:passed`, `path:lab1`, or `title:vector`. Combine them, for example `course:pc year:26/27 status:passed`. Exact names/codes and word matches rank above loose fuzzy matches; equally relevant results show newer academic years first. Catalog years are also listed newest first. This searches downloaded metadata, not undiscovered server content.
- Search supports Left/Right, Home/End, Ctrl+W (delete word), and Ctrl+U (delete before the cursor). Up/Down or Ctrl+P/Ctrl+N select a result; Enter opens it and Escape restores browsing. Search uses a compact centered picker with aligned title/course/year columns. Only the selected result shows its full location and grading outcome; cache labels and the background workspace panel stay out of search. Press **F1** to show or hide filter examples. Search and navigation controls stay visible.
- Recent locations are deduplicated and limited to 20, stored separately in `~/.config/themis/browser.json`.
- **Backspace** restores the previous browsing location, selection, and expanded branches. **p** opens the linked target even when it is outside the current tree.
- **P** opens project context: launch directory, inherited link directory, exact configuration file, linked target, and resolved download destination. **l** links the selected target in the chosen directory; **c** changes the effective inherited link; **x** removes only its configuration file. **/** edits the new link directory (an existing directory, defaulting to the launch directory). Each mutation shows the exact file and target for review; Enter confirms and Escape cancels. Removing a child link can reveal a parent association.
- Browsing leaves the directory link, working directory, and file destination unchanged. File selection shows the absolute download destination and flags browsing outside the linked target. Changing a link explicitly re-resolves project context. Link targets must belong to the configured Themis instance.
- The context footer shows the current remote location and linked directory when space permits. The project screen remains scrollable on narrow terminals.

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

### Choose download folders

Press **d** on an assignment to select files, then **Enter** (or **o**) to choose where they go. The folder picker starts inside the linked project, even when Themis was launched from a deeper subdirectory. Without a link it uses the launch directory.

- Select a child folder and press **Enter** or **Right** to open it; **Left** returns to its parent. Select **Use this folder** to review the destination.
- Press **n** to create a folder. Nested names such as `pc-lab-07/pc-lab-07-e01` create the whole structure and open the new folder.
- Press **/** to enter an existing folder path inside the project. Symlink folders are excluded, and navigation stays inside the project root.
- Review shows the chosen folder, exact relative output filenames, and any existing files that will be replaced. **Enter** starts downloading; **Escape** returns to folder selection. Escape from the picker returns to file selection without changing the destination.
- Each assignment remembers its own folder in `.themis/project.json` (`download_dirs`, relative to the link directory). A different assignment starts at the project root rather than inheriting an unrelated exercise folder. Folder creation takes effect when confirmed; leaving the picker does not remove folders you created.

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
- `--non-interactive`
- `--username`
- `--password`
- `--totp`

`auth` flags:
- `--totp-secret-stdin`
- `--clear-totp-secret`
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

## Agent-facing workflows

The CLI can inspect an existing project, search assignments, read PDFs/text, and prepare lab materials without driving the TUI. Start with `themis capabilities --json`.

```sh
themis project inspect --dir /path/to/pc-assignments --json
themis search 'lab 7' --dir /path/to/pc-assignments --refresh --json
themis show --target 'LAB_URL_FROM_SEARCH' --refresh --depth 1 --json
themis setup plan --dir /path/to/pc-assignments --target 'LAB_URL_FROM_SEARCH' \
  --into pc-lab-07 --out /tmp/lab7-plan.json --json
themis setup apply --dir /path/to/pc-assignments --plan /tmp/lab7-plan.json --json
```

Setup targets existing workspaces. Agents can inspect earlier lab directories, read the actual assignment materials, and pass explicit exercise-folder mappings. Planning reports conflicts; applying preserves modified files by default and supports resuming partial downloads. Search reports partial catalog coverage and keeps unknown submission states distinct.

See [Agent CLI](docs/agent-cli.md) for all commands, mapping files, document pagination, JSON/error contracts, and freshness controls. These commands use a new versioned JSON envelope; the legacy command formats below remain unchanged.

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

### Submit files and watch grading

Select an assignment in the TUI and press **u**. The local picker starts in the
current directory. Space selects a file or an entire folder, `l` opens a folder,
`h` goes to its parent, and `/` enters a directory path. Tab switches between the
picker and its source/PDF preview; narrow terminals show one pane at a time.

Enter prepares a private snapshot and opens review. Review lists the submitting
account/group, language, exact filenames, combined size, server limits, and dates.
Use `L` to change the language. **Tab selects Submit; Enter activates it.** The
initial action is Back. Uploading happens only after this explicit action.

The equivalent CLI flow is:

```sh
# Validate and inspect the manifest without posting anything.
./themis submit --assignment-url 'https://themis.housing.rug.nl/course/YEAR/COURSE/EXERCISE' \
  --file ./main.c --folder ./support --dry-run

# Review interactively, submit, and watch grading.
./themis submit --assignment-url 'https://themis.housing.rug.nl/course/YEAR/COURSE/EXERCISE' \
  --file ./main.c --folder ./support

# Inspect or resume watching a concrete submission URL returned by Themis.
./themis status --submission-url 'https://themis.housing.rug.nl/submission/…/@submissions/GROUP/ID' --watch
```

`--file` and `--folder` are repeatable. Folders retain their folder-relative names;
`.git`, `.themis`, and symlinks are excluded. Archives are uploaded as ordinary
files. Server filename rules, file count, and **combined byte limits** are read
from the live form. One permitted language is chosen automatically; otherwise
an unambiguous extension match is suggested, or choose `--language LANGUAGE`.
Changed server requirements require another review before uploading.

Noninteractive uploads require `--yes`. `--dry-run` never posts. Submission
watching defaults to true; use `--watch=false` to return after acceptance.
`--timeout 15m` controls the watch deadline. `--json` produces one final JSON
object on stdout, with the manifest and progress on stderr during an upload.
Receipts include the concrete submission URL and a status link.

In the TUI, **v** opens the selected assignment's remembered submission or latest
result. Results distinguish scores from execution progress, list individual test
verdicts, and preview compiler logs, output, and diffs. Use `j/k` to select an
artifact, Tab to preview it, and Page Up/Down to scroll results. Grading monitoring
continues after Escape returns to browsing. Opening a different result pauses the
previous watch. `r` resumes a paused watch; when focused on a preview, `r` reloads
that preview. Quitting stops local monitoring, not server grading.

Polling uses two-second intervals and backs off to 4, 8, 16, then 30 seconds on
transient errors. It pauses after five consecutive failures and stops on a final
verdict, an explicit unjudged state, session expiry, or the watch deadline.
Compilation errors are terminal even if individual tests have no verdict.

Uploads are never automatically retried. If acceptance cannot be established,
the client reports **Submission outcome unknown** and preserves a status link.
Inspect that status before considering another upload. A generic history page or
an older result is not treated as proof that the upload succeeded.

Receipts and last known results are saved atomically in
`~/.config/themis/submissions.json` with private permissions, separately from course
state. CSRF tokens and source contents are not saved there. Reviewed source
snapshots are private temporary files and are removed on completion/cancellation.
Assignment statistics refresh after acceptance and final grading.

| Exit code | Meaning |
| --- | --- |
| 0 | Accepted without waiting, passed, or successful preparation/status inspection |
| 1 | Request or preparation failure |
| 2 | Terminal unsuccessful grading, including explicitly unjudged |
| 3 | Watch timeout, paused watch, or uncertain submission outcome |
| 130 | User interruption |

This workflow supports ordinary file submission forms. It does not support exam
or quiz answer forms, automatic resubmission, watch-on-save, remote cancellation,
or rejudging. Use only local test servers for automated upload validation; a real
submission requires a designated exercise and explicit authorization.
