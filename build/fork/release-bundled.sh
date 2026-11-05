#!/usr/bin/env bash
# Keep the upstream Go recipes; select their embedded helper before each target.
set -euo pipefail
helpers=${1:?usage: release-bundled.sh HELPERS_DIRECTORY SOURCE_SHA VERSION}
sha=${2:?missing immutable source commit}
version=${3:?missing version}
# Windows x64, macOS Apple Silicon and Linux x64 only.
for target in darwin/arm64 linux/amd64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}
  node build/fork/stage-cursor-backend.mjs "$helpers/cursor-backend-$os-$arch" "$os" "$arch" "$sha"
  make release-cli TARGETS="$target" VERSION="$version"
done
node build/fork/stage-cursor-backend.mjs "$helpers/cursor-backend-windows-amd64" windows amd64 "$sha"
make release-windows WINDOWS_ARCHES=amd64 VERSION="$version"
