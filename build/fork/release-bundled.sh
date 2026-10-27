#!/usr/bin/env bash
# Keep the upstream Go recipes; select their embedded helper before each target.
set -euo pipefail
helpers=${1:?usage: release-bundled.sh HELPERS_DIRECTORY SOURCE_SHA VERSION}
sha=${2:?missing immutable source commit}
version=${3:?missing version}
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64 android/arm64; do
  os=${target%/*}; arch=${target#*/}
  node build/fork/stage-cursor-backend.mjs "$helpers/cursor-backend-$os-$arch" "$os" "$arch" "$sha"
  make release-cli TARGETS="$target" VERSION="$version"
done
for arch in amd64 arm64; do
  node build/fork/stage-cursor-backend.mjs "$helpers/cursor-backend-windows-$arch" windows "$arch" "$sha"
  make release-windows WINDOWS_ARCHES="$arch" VERSION="$version"
done
