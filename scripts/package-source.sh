#!/usr/bin/env bash
set -euo pipefail
version=${1:?usage: package-source.sh VERSION OUTPUT_DIR}
output_dir=${2:?missing output directory}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Expected a stable semantic version' >&2; exit 1; }
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# git archive honors .gitattributes; only committed source enters a release.
git archive --format=tar --prefix="themis-$version/" HEAD | tar -xf - -C "$work"
(cd "$work/themis-$version" && go mod vendor)
tar -czf "$output_dir/themis_${version}_source.tar.gz" -C "$work" "themis-$version"
