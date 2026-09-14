# Themis CLI Skills

This file describes practical skills/workflows for using `themis` effectively in this repository.

## 1) Verify Access And Auth

Use this first whenever cookies/base URL may be stale.

```sh
./themis check \
  --base-url "https://themis.housing.rug.nl" \
  --cookie-file "$HOME/.config/themis/cookie.txt"
```

Expected result: authenticated user info and `status=ok`.

## 2) Discover Course/Assignments Tree

Build or update local state for a course root.

```sh
./themis list \
  --base-url "https://themis.housing.rug.nl" \
  --cookie-file "$HOME/.config/themis/cookie.txt" \
  --discover \
  --root-url "https://themis.housing.rug.nl/course/2025-2026/os/" \
  --discover-depth 6
```

Use refresh modes:
- Node-only refresh: `--refresh-url <url> --refresh-depth 0`
- Subtree refresh: `--refresh-url <url> --refresh-depth <n>`
- Full refresh from root: `--full-refresh`

## 3) Link Project To A Course Root

Set repo-local defaults so TUI and state-first flows resolve root without retyping.

```sh
./themis project link \
  --base-url "https://themis.housing.rug.nl" \
  --root-url "https://themis.housing.rug.nl/course/2025-2026/os" \
  --default-refresh-depth 1
```

Writes `.themis/project.json`.

## 4) Find Test Indices For An Exercise

Probe available `N.in`/`N.out` pairs.

```sh
./themis list \
  --base-url "https://themis.housing.rug.nl" \
  --cookie-file "$HOME/.config/themis/cookie.txt" \
  --tests-url "https://themis.housing.rug.nl/file/.../%40tests/1.in" \
  --start 1 --max 50 --max-misses 5
```

## 5) Download Tests/Assets

Fetch discovered test pairs:

```sh
./themis fetch \
  --base-url "https://themis.housing.rug.nl" \
  --cookie-file "$HOME/.config/themis/cookie.txt" \
  --tests-url "https://themis.housing.rug.nl/file/.../%40tests/1.in" \
  --out ./tests
```

Path behavior:
- `%40tests` assets go under `./tests/...`
- Other files go under current working directory
- Path-like assets keep relative paths (example: `./imgs/1.img`)

## 6) Browse In TUI (State-First)

```sh
./themis tui \
  --base-url "https://themis.housing.rug.nl" \
  --cookie-file "$HOME/.config/themis/cookie.txt"
```

Useful keys:
- `j/k`: move
- `h/l`: fold/unfold
- `enter`: open/select
- `r`: refresh node
- `R`: refresh subtree
- `f`: full refresh
- `d`: download
- `q`: quit

## 7) Assignment Stats Skill

Every assignment/exercise can have a stats page at `/stats/...`.

Example:
- Course page: `https://themis.housing.rug.nl/course/2025-2026/os/lab5/5_file_writer`
- Stats page: `https://themis.housing.rug.nl/stats/2025-2026/os/lab5/5_file_writer`

In TUI details pane, use stats info to inspect:
- pass/fail/not-submitted style status
- stale/fresh metadata (`fetched_at`)
- grade/count summary fallback when pass metrics are unavailable

## 8) Machine-Readable Mode

Add `--json` to commands for automation.

Common fields:
- `status`
- `base_url`
- `error` (on failure)

Command-specific fields:
- `authenticated`, `user` (`check`)
- `assignments`, `refreshed`, `refresh_scope` (`list --discover`)
- `tests_base_url` (`list`, `fetch`)
- `target_dir` (`fetch`)
