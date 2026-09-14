Add a navigation API client in `internal/discovery`.
Implement `GET /api/navigation{path}` per the contract in [`themis-api-breakdown.md:253`](file:///Users/daniel/code/personal-repos/themis-cli/themis-api-breakdown.md#L253), with a typed response struct for `path`, `title`, `submitable`, `isExam`, `isQuiz`, and `visible`.

2. Split “node metadata fetch” from “child enumeration”.
Keep HTML page fetch/parsing for assignment metadata, assets, breadcrumbs, and stats links, but stop using HTML as the source of child nodes. The main seam is [`fetchPageSnapshot`](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/refresh.go) at [internal/discovery/refresh.go:278](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/refresh.go#L278).

3. Replace `extractChildren` with navigation-derived children.
For a canonical `/course...` URL, derive or reuse `NavAPIURL`, call `/api/navigation...`, and map each JSON item into the existing `pageChild` shape. `NavAPIURL` is already a first-class field on state nodes at [`internal/state/model.go:38`](file:///Users/daniel/code/personal-repos/themis-cli/internal/state/model.go#L38), so the state model does not need a structural change.

4. Preserve lazy subtree loading semantics.
Keep `RefreshNode(..., depth)` behavior the same: fetch the target node, hydrate its direct children from the navigation API, and only recurse into child pages when `remainingDepth > 0`. That aligns with the docs’ “request children on demand” guidance at [`themis-api-breakdown.md:300`](file:///Users/daniel/code/personal-repos/themis-cli/themis-api-breakdown.md#L300).

5. Define root-path handling explicitly.
Support catalog root `/course` by querying `/api/navigation/`, and support nested course paths by querying `/api/navigation/<path>`. Centralize URL/path conversion so `/course/...` and `/api/navigation/...` stay consistent.

6. Keep HTML fetches for nodes you actually recurse into.
If `depth=0`, a refresh should still fetch the target page once for metadata, but child enumeration should come from the nav API. If `depth>0`, recurse into children as today, but each parent’s child list should come from JSON, not DOM parsing.

7. Attach navigation metadata to child nodes.
When upserting child placeholders in [`RefreshNode`](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/refresh.go) around [internal/discovery/refresh.go:178](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/refresh.go#L178), populate `Title`, `NavAPIURL`, and a small `details["navigation"]` block for `submitable/isExam/isQuiz/visible` if those flags are useful later in TUI or filters.

8. Update and expand tests around refresh behavior.
Most existing tests in [`internal/discovery/refresh_test.go`](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/refresh_test.go) assume HTML children. Replace those fixtures with mixed responses: HTML for node pages, JSON for `/api/navigation...`. Add coverage for:
- root `/api/navigation/`
- subtree refresh using `/api/navigation/<path>`
- child replacement / tombstones still working
- invisible or non-submittable nodes
- failure behavior when nav API fails but page fetch succeeds

9. Decide what to do with the legacy HTML discovery helper.
Either delete [`DiscoverAssignments`](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/discovery.go#L83) / [`getAssignmentsOnPage`](file:///Users/daniel/code/personal-repos/themis-cli/internal/discovery/discovery.go#L137), or refactor them to use the same navigation client so there is one discovery mechanism.

**Recommended Implementation Order**

1. Add `fetchNavigationChildren(canonicalCourseURL, navAPIURL)` and tests.
2. Change `fetchPageSnapshot` to source `Children` from that function.
3. Keep `extractCurrentNavAPIURL` as fallback, but stop using `extractChildren`.
4. Update refresh tests.
5. Remove or rewrite the legacy HTML discovery path.

No code changed yet; this was inspection and planning only.
