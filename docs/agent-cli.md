# Agent-facing CLI

The agent CLI operates on an **existing workspace**. The agent can inspect earlier labs with its usual filesystem tools, read the actual Themis assignment, then provide explicit folder mappings. The CLI does not create Git repositories, infer a repository's naming convention, solve exercises, or submit code as part of setup.

## Output and command behavior

All new commands produce a versioned JSON object. `--json` selects a compact single-line object; without it, JSON is pretty-printed. No new command opens the TUI or requests terminal input. Online operations reuse saved cookies; when explicitly configured with a saved username, password, and authenticator key, they can renew an expired session automatically.

```json
{
  "schema_version": 1,
  "status": "ok",
  "data": {},
  "warnings": [],
  "error": null
}
```

`error` is omitted on success. On failure it includes `code`, `message`, and, where applicable, `recovery`. Exit codes are 0 for success, 1 for failure, 3 for a partially applied setup, and 130 for cancellation. Existing submission commands and their exit-code/JSON contracts remain unchanged.

Common flags include `--dir` (existing workspace), `--base-url`, `--session-file`, `--state-file` (isolated catalog cache), `--timeout`, and `--json`. Search flags can appear before or after its positional query. Use `themis COMMAND --help` or `themis capabilities --json` for discovery.

IDs and URLs come from returned data. Mutating operations use exact identities; they do not pick the highest-ranked fuzzy match. An ambiguous target returns `AMBIGUOUS_TARGET` with candidate identities rather than silently choosing a course/year.

## Commands

| Command | Behavior |
| --- | --- |
| `capabilities` | Lists supported noninteractive commands and limits; does not require a workspace or credentials. |
| `auth status` | Checks the saved session; returns `AUTH_REQUIRED` and login instructions when necessary. Can renew an expired session with configured credentials. Does not output cookies, passwords, authenticator keys, or CSRF tokens. |
| `project inspect --dir DIR` | Reports the nearest effective project link, its configuration path, known assignment directories, and up to 200 immediate local directory names. Works independently of the catalog cache. |
| `project link --dir DIR --root-url URL` | Links an existing directory, preserving remembered download choices. This existing command retains its previous JSON contract. |
| `project unlink --dir DIR` | Removes the effective link file, including an inherited one, then reports the newly effective association. Inspect the configuration path before invoking. |
| `catalog list --parent ID_OR_URL` | Returns one level of children; academic years are newest first. Defaults to the course catalog. |
| `search QUERY` | Searches downloaded titles, acronyms, course codes, years, paths, and known grading states. Defaults to the linked course; `--all` searches the whole cache. |
| `show --target ID_OR_URL` | Returns the target, child identities, description, normalized configuration, assets, freshness, and discovery coverage. `--requirements` also reads the live ordinary submission form. |
| `assets list --target ID_OR_URL` | Lists assets with stable asset IDs, source assignment identities, and inherited/shared-document attribution. |
| `assets read --asset ASSET_ID` | Reads a known asset as paginated text or PDF text, without writing local files. `--target` can constrain the asset lookup. |
| `setup plan --target ID_OR_URL --into RELATIVE_DIR` | Plans a lab/exercise subtree, shared statements, folders, downloads, and assignment mappings. |
| `downloads plan --target ID_OR_URL --into RELATIVE_DIR` | Plans only the selected node's downloadable assets. Repeat `--asset-id` to select specific assets, including explicitly requested shared parent assets. |
| `setup apply --plan FILE` | Applies a reviewed setup plan inside the same existing workspace. |
| `downloads apply --plan FILE` | Applies a reviewed download plan. |

### Discovery, scopes, and unknown data

Search, show, and catalog listing default to cached data. `--refresh` fetches only the selected URL/scope, bounded by `--depth` (default 0) and `--max-nodes` (default 200, maximum 1,000). `setup plan --refresh` defaults to depth 8 within the selected lab/exercise; it cannot crawl a course as a setup target. Plans are limited to 2,000 files; select a smaller target or explicit assets for larger setups. Use `--limit` (1–200) and `--offset` for paginated search, catalog, asset-list, and show results; show reports separate child/asset totals and continuation offsets. Detailed targets cap inline child IDs at 200 and report `child_ids_truncated`, so use catalog pagination for the full list. `next_offset: null` indicates the last page.

Search supports `--course ID_OR_URL`, `--year`, and `--kind course|assignment|assignment_group|exercise|unknown`. Its free-text query also supports `course:`, `year:`, `type:`, `status:`, `path:`, and `title:`. Newer years break ties between equally relevant matches.

Inspect `coverage.complete`, `unloaded_nodes`, and warnings. An empty partial search does not establish that a lab is absent. Unknown grading data remains `unknown`; a status-page link alone does not establish `not_submitted`. A node whose children have not been loaded has an unknown semantic role rather than being assumed to be an exercise. Cached grading is not a live account check.

`show` and `assets list` also return `missing_material_ancestors`: exact course/lab URLs whose shared materials may not have been loaded. Inspect or refresh those pages to collect additional context. Setup planning warns when parent materials are incomplete. It includes known shared parent PDFs by default, and avoids downloading the same shared PDF once per exercise.

### Reading assignment documents

```sh
# Replace IDs with values returned by show/assets list.
themis assets read --asset 'asset:ID_FROM_LIST' --start-page 1 --pages 5 --json
themis assets read --asset 'asset:ID_FROM_LIST' --start-line 1 --lines 200 --json
```

PDF output contains numbered `pages` and `next_page`. Text output contains `text`, `start_line`, and `next_line`. Follow continuation fields until null/absent. `complete` means this response covers the entire source from its beginning; a later final range can have no continuation while still having `complete: false`.

Reads are capped at 8 MiB of source data and 1 MiB of returned text, with 1–20 PDF pages or 1–2,000 text lines per request. PDF reading uses Poppler's `pdftotext`; missing tools, encrypted/damaged PDFs, and ranges without extractable text return errors. No OCR is performed. Text whose source exceeds the byte cap is explicitly marked `source_truncated`; it must not be treated as the full assignment. Use downloads for binary/oversized assets. External asset hosts outside the configured instance's `/file/` and `/download/` routes are not supported by these agent commands.

## Example: prepare lab 7 in an existing repository

```sh
# Resolve the existing link and observe earlier lab directories.
themis project inspect --dir /path/to/pc-assignments --json

# Find candidates within the linked course/year. Read IDs/URLs from the result.
themis search 'lab 7' --dir /path/to/pc-assignments --refresh --json

# Load the chosen lab and its exercises. Larger/nested labs can use a deeper
# targeted refresh, or setup plan --refresh.
themis show --dir /path/to/pc-assignments --target 'LAB_URL_FROM_SEARCH' \
  --refresh --depth 1 --json

themis assets list --dir /path/to/pc-assignments \
  --target 'LAB_URL_FROM_SEARCH' --json

# Read the PDF/exercise materials before deciding the local structure.
themis assets read --asset 'PDF_ID_FROM_LIST' --pages 5 --json

# Plan files inside the existing repository. This does not create lab folders.
themis setup plan --dir /path/to/pc-assignments \
  --target 'LAB_URL_FROM_SEARCH' --into pc-lab-07 \
  --mapping /tmp/pc-lab-07-mapping.json \
  --out /tmp/pc-lab-07-plan.json --json

# Inspect the plan, then apply it. --refresh rechecks the exact source pages
# against the current server before comparing the planned fingerprints.
themis setup apply --dir /path/to/pc-assignments \
  --plan /tmp/pc-lab-07-plan.json --refresh --json
```

The optional mapping file is a JSON object from exercise IDs **or canonical URLs** to directories relative to `--dir`:

```json
{
  "EXERCISE_1_ID_FROM_SHOW": "pc-lab-07/pc-lab-07-e01",
  "EXERCISE_2_ID_FROM_SHOW": "pc-lab-07/pc-lab-07-e02"
}
```

Without mappings, setup preserves the remote hierarchy's URL slugs beneath `--into`. It does not invent exercise numbers or copy solutions from earlier labs. The agent chooses mappings after inspecting the existing repository; the CLI validates and executes those explicit choices. A missing `--into`, missing workspace, unloaded subtree, conflicting mappings, or unsafe path fails explicitly.

## Plan/apply guarantees

- Planning reads the workspace and cache. Only an explicitly supplied `--out` writes a plan file; its parent directory must already exist.
- Plans bind the workspace root, exact source identities/fingerprints, asset mappings, current project configuration, and existing file hashes. Edited/unsupported plan files and changed reviewed inputs are rejected.
- Source checks use the local cache by default. `apply --refresh` first refreshes the plan's exact source pages to detect server-side metadata changes. Asset bytes without a published hash cannot be fingerprinted before downloading; downloaded hashes are recorded afterwards.
- Existing unowned or modified files become `conflict` actions. Apply blocks those plans before downloads. `--overwrite` belongs on **plan**, where replacements and their existing hashes are visible for review; a later local edit still invalidates the replacement.
- Files are downloaded into private temporary locations and published only when complete. Newly appearing destinations are not silently overwritten. Symlink/traversal/reserved metadata paths are rejected.
- `.themis/setup-manifest.json` records asset URLs and hashes after each successful download. Reapplying the same plan skips verified owned files and retries failed downloads. Partial failures return per-file receipts and `PARTIAL_FAILURE`; no automatic upload or submission occurs.
- Setup preserves the existing directory link and its preferences. If no link exists, the plan explicitly proposes a course link. A mismatched existing link returns `PROJECT_MISMATCH`. Successful setup records exercise-folder mappings in the effective project's configuration, including inherited links.

Existing `submit --dry-run`, reviewed upload, and `status --watch` remain available for subsequent, separately requested submission workflows.

## Optional unattended authentication

Configure this locally once, outside the agent chat: log in with username/password remembering enabled, then run `themis auth --totp-secret-stdin`. Enter the authenticator setup key at the hidden prompt, or supply it from a secret manager through stdin. Do not put the key into command arguments, source files, or agent messages.

Online agent operations now renew expired sessions once using the stored password and a locally generated current TOTP code. Valid cookies are reused. Authentication failures still return structured errors; network/server failures do not trigger credential resubmission. Cancellation and command deadlines cover the login flow. This never retries a submitted upload.

`themis auth --json` reports `totp_secret_stored` and the bound instance without exposing the key. `themis login --non-interactive --json` explicitly establishes a session without prompts. `themis auth --clear-totp-secret` removes the stored key. Credentials live unencrypted in the private, atomically saved `0600` session file; password plus authenticator key permits full login access. See the README for supported key formats and account binding.
