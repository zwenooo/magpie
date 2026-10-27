#!/usr/bin/env bash
# Exercise the real generator with offline checksums before publishing a tap.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
assets=(magpie-cli-darwin-arm64 magpie-cli-darwin-amd64 magpie-cli-linux-arm64 magpie-cli-linux-amd64 magpie-darwin-arm64.zip magpie-darwin-amd64.zip)
for i in "${!assets[@]}"; do printf '%064x  %s\n' "$((i + 1))" "${assets[$i]}"; done > "$work/sums"
TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/out" bash "$root/scripts/update-tap.sh" 1.2.3
for asset in "${assets[@]:0:4}"; do
  hash=$(awk -v name="$asset" '$2 == name {print $1}' "$work/sums")
  grep -F -A1 "/v1.2.3/$asset\"" "$work/out/Formula/magpie.rb" | grep -Fq "$hash"
done
grep -Fq 'https://github.com/zwenooo/magpie/releases/download/' "$work/out/Casks/magpie-app.rb"
! grep -Fq 'auto_updates true' "$work/out/Casks/magpie-app.rb"
for spec in 'arm:5' 'intel:6'; do
  hash=$(printf '%064x' "${spec#*:}")
  grep -E "${spec%:*}:.*$hash" "$work/out/Casks/magpie-app.rb" > /dev/null
done
head -n 5 "$work/sums" > "$work/missing"
cat "$work/sums" "$work/sums" > "$work/duplicate"
for bad in missing duplicate; do
  if TAP_SUMS_FILE="$work/$bad" TAP_OUT_DIR="$work/$bad-out" bash "$root/scripts/update-tap.sh" 1.2.3; then
    echo "invalid checksum list accepted: $bad" >&2; exit 1
  fi
  test ! -e "$work/$bad-out"
done
if TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/invalid" bash "$root/scripts/update-tap.sh" 'bad/version'; then
  echo 'invalid version accepted' >&2; exit 1
fi
if TAP_RELEASE_REPO=yetone/magpie-releases TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/upstream" bash "$root/scripts/update-tap.sh" 1.2.3; then
  echo 'upstream release source accepted' >&2; exit 1
fi
echo 'Homebrew generation checks passed'
