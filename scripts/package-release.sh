#!/usr/bin/env bash
set -euo pipefail

# Run from the repository root with Go on PATH. No user configuration is read.
version=${1:?usage: package-release.sh VERSION GOOS GOARCH OUTPUT_DIR}
target_os=${2:?missing GOOS}
target_arch=${3:?missing GOARCH}
output_dir=${4:?missing output directory}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Expected a stable semantic version' >&2; exit 1; }
case "$target_os/$target_arch" in
  darwin/arm64|darwin/amd64|linux/arm64|linux/amd64) ;;
  *) echo 'Unsupported release platform' >&2; exit 1 ;;
esac
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
revision=$(git rev-parse HEAD)
CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath \
  -ldflags="-s -w -X main.version=$version -X main.commit=$revision" \
  -o "$work/themis" ./cmd/themis
if [[ "$target_os" == darwin ]]; then
  [[ "$(uname -s)" == Darwin ]] || { echo 'macOS archives must be built on macOS for ad-hoc signing' >&2; exit 1; }
  codesign --force --sign - "$work/themis"
  codesign --verify "$work/themis"
fi
cp LICENSE README.md "$work/"
mkdir -p "$work/docs"
cp docs/agent-cli.md "$work/docs/"
printf 'Corresponding source, including vendored dependencies:\nhttps://github.com/danielgrbacbravo/themis/releases/download/v%s/themis_%s_source.tar.gz\n' "$version" "$version" > "$work/SOURCE.txt"
# Include third-party notices without shipping module caches or local settings.
go mod vendor -o "$work/vendor"
python3 - "$work" <<'PY'
import pathlib, shutil, sys
root = pathlib.Path(sys.argv[1])
vendor = root / 'vendor'
for source in vendor.rglob('*'):
    if source.is_file() and source.name.upper().startswith(('LICENSE', 'NOTICE', 'COPYING', 'COPYRIGHT', 'AUTHORS')):
        dest = root / 'licenses' / source.relative_to(vendor)
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, dest)
shutil.rmtree(vendor)
PY
tar -czf "$output_dir/themis_${version}_${target_os}_${target_arch}.tar.gz" -C "$work" .
