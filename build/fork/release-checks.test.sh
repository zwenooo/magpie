#!/usr/bin/env bash
# Exercise release boundaries without GitHub, credentials or real binaries.
set -euo pipefail
scripts=$(cd "$(dirname "$0")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
git init -q -b main "$fixture/source"
git -C "$fixture/source" config user.name 'Release test'
git -C "$fixture/source" config user.email 'release-test@example.invalid'
printf 'main\n' > "$fixture/source/source.txt"
git -C "$fixture/source" add source.txt
git -C "$fixture/source" commit -qm main
main_sha=$(git -C "$fixture/source" rev-parse HEAD)
git -C "$fixture/source" tag v1.2.3
git -C "$fixture/source" checkout -qb feature
printf 'feature\n' >> "$fixture/source/source.txt"
git -C "$fixture/source" commit -qam feature
git -C "$fixture/source" tag v1.2.4
git -C "$fixture/source" checkout -q main
git clone -q "$fixture/source" "$fixture/work"

metadata() {
  local expected=$1 ref=$2 version=$3 publish=$4 event=${5:-workflow_dispatch} event_ref=${6:-refs/heads/main}
  : > "$fixture/output"
  if (
    cd "$fixture/work"
    GITHUB_REPOSITORY=zwenooo/magpie GITHUB_SHA="$main_sha" \
      GITHUB_EVENT_NAME="$event" GITHUB_REF="$event_ref" GITHUB_OUTPUT="$fixture/output" \
      INPUT_REF="$ref" INPUT_VERSION="$version" INPUT_PUBLISH="$publish" \
      bash "$scripts/release-metadata.sh" > "$fixture/log" 2>&1
  ); then
    [[ "$expected" == pass ]] || { cat "$fixture/log"; echo 'Unsafe release request passed.' >&2; exit 1; }
  else
    [[ "$expected" == fail ]] || { cat "$fixture/log"; echo 'Valid release request failed.' >&2; exit 1; }
  fi
}
metadata pass main '' false
grep -Fx "sha=$main_sha" "$fixture/output" >/dev/null
grep -Fx "version=fork-${main_sha:0:8}" "$fixture/output" >/dev/null
grep -Fx 'publish=false' "$fixture/output" >/dev/null
metadata pass main '1.2.3-rc.1' false
grep -Fx 'version=1.2.3-rc.1' "$fixture/output" >/dev/null
metadata fail main '1.2.3+build.2' false
metadata pass refs/tags/v1.2.3 '' true
grep -Fx 'tag=v1.2.3' "$fixture/output" >/dev/null
metadata pass '' '' false push refs/tags/v1.2.3
grep -Fx 'publish=true' "$fixture/output" >/dev/null
metadata pass '' '' true workflow_dispatch refs/tags/v1.2.3
grep -Fx 'version=1.2.3' "$fixture/output" >/dev/null
metadata fail '' '' false push refs/tags/v1.2.4
metadata fail $'main\npublish=true' 1.2.3 true
metadata fail --upload-pack 1.2.3 false
metadata fail main $'1.2.3\npublish=true' false
metadata fail main '1.2.3$(touch injected)' false
metadata fail main '' true
metadata fail main 1.2.4 true
metadata fail refs/tags/v1.2.4 '' true

mkdir "$fixture/assets"
for os in darwin linux windows; do
  for arch in amd64 arm64; do
    ext=
    [[ "$os" != windows ]] || ext=.exe
    printf 'cli\n' > "$fixture/assets/magpie-cli-$os-$arch$ext"
  done
done
printf 'android\n' > "$fixture/assets/magpie-cli-android-arm64"
for arch in amd64 arm64; do
  printf 'app\n' > "$fixture/assets/magpie-windows-$arch.exe"
  printf 'app\n' > "$fixture/assets/magpie-linux-$arch"
  for ext in dmg zip; do printf 'app\n' > "$fixture/assets/magpie-darwin-$arch.$ext"; done
done
bash "$scripts/release-assets.sh" "$fixture/assets" > "$fixture/log"
[[ $(wc -l < "$fixture/assets/SHA256SUMS") == 15 ]]
printf 'unexpected\n' > "$fixture/assets/magpie-linux-amd64.new"
if bash "$scripts/release-assets.sh" "$fixture/assets" > "$fixture/log" 2>&1; then
  echo 'An unexpected release asset was accepted.' >&2; exit 1
fi
rm "$fixture/assets/magpie-linux-amd64.new"
: > "$fixture/assets/magpie-cli-android-arm64"
if bash "$scripts/release-assets.sh" "$fixture/assets" > "$fixture/log" 2>&1; then
  echo 'An empty release asset was accepted.' >&2; exit 1
fi
printf 'Release metadata and exact asset checks passed.\n'
