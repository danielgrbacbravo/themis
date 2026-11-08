# Releases and Homebrew

The public source repository is https://github.com/danielgrbacbravo/themis.
The public tap is https://github.com/danielgrbacbravo/homebrew-themis.

## Publish a stable release

After reviewing and testing a clean commit on main:

```sh
git tag -a v0.1.1 -m 'Themis v0.1.1'
git push origin v0.1.1
```

Use a new `vMAJOR.MINOR.PATCH` tag for each release; do not move published tags.
The release workflow tests on macOS/Linux, builds pure-Go binaries for each
platform's ARM64 and x86-64 architectures, and publishes four `.tar.gz` binary
archives, a source archive, and `SHA256SUMS`. Nothing is published until all
build and source jobs succeed. A failed tag workflow can be rerun from Actions.
Only the final publication job receives repository write permission.

Every binary has `themis --version` and `themis version --json` metadata.
macOS downloads are ad-hoc signed for executable integrity; they are not Apple
Developer ID signed or notarized. Homebrew builds locally from source. Do not
instruct users to disable Gatekeeper. Windows is not a tested release target.

Source archives include vendored dependencies and their license notices.
`.gitattributes` excludes the local development executable, captured HTML,
logs, macOS metadata, and unrelated research documents. Binary packages include
the project license, third-party notices, CLI documentation, and a corresponding
source download link. Packaging never reads a user's session or project files.

## Homebrew updates

The tap contains `Formula/themis.rb`, `scripts/update.py`, and its own workflows.
Its updater runs every six hours and via manual dispatch, reads the latest
public stable release, verifies the source archive checksum, and commits the
new pinned version. It uses the tap's own `GITHUB_TOKEN`; no cross-repository
secret is needed. GitHub can delay scheduled jobs and disables schedules on
inactive public repositories; use manual dispatch if necessary.

For immediate publication after a release:

```sh
gh workflow run update.yml --repo danielgrbacbravo/homebrew-themis
```

The tap's `test.yml` installs and tests the formula on macOS and Linux.
GitHub suppresses push-triggered workflows for bot-token commits, so manually
run `test.yml` after an automated formula update when verifying a release.

The formula builds offline from the vendored source using Homebrew's Go build
helper, installs `poppler` for PDF support, and performs credential-free smoke
tests. Upgrades and uninstalls leave user configuration untouched.

## Local packaging checks

With Go and Python 3 available, run from the repository root:

```sh
go test -timeout 5m ./...
bash scripts/package-release.sh 0.1.0 darwin arm64 dist
bash scripts/package-source.sh 0.1.0 dist
```

Use the appropriate OS/architecture; macOS binaries must be packaged on macOS
for signing. Source packaging uses the committed HEAD, not uncommitted edits.
