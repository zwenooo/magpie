#!/usr/bin/env bash
# Upstream's public asset contract: 15 binaries/archives plus SHA256SUMS.
set -euo pipefail
cd "${1:?usage: release-assets.sh DIST}"
expected=(
  magpie-cli-android-arm64
  magpie-cli-darwin-amd64 magpie-cli-darwin-arm64
  magpie-cli-linux-amd64 magpie-cli-linux-arm64
  magpie-cli-windows-amd64.exe magpie-cli-windows-arm64.exe
  magpie-darwin-amd64.dmg magpie-darwin-amd64.zip
  magpie-darwin-arm64.dmg magpie-darwin-arm64.zip
  magpie-linux-amd64 magpie-linux-arm64
  magpie-windows-amd64.exe magpie-windows-arm64.exe
)
for asset in "${expected[@]}"; do
  [[ -s "$asset" && ! -L "$asset" ]] || { echo "Missing or empty release asset: $asset" >&2; exit 1; }
done
# Ignore only an existing checksum file, making the check safe to repeat.
diff -u <(printf '%s\n' "${expected[@]}" | LC_ALL=C sort) <(find . -mindepth 1 -maxdepth 1 ! -name SHA256SUMS -printf '%f\n' | LC_ALL=C sort)
sha256sum "${expected[@]}" > SHA256SUMS
sha256sum --check SHA256SUMS
