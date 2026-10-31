#!/usr/bin/env bash
# Exercise release boundaries without GitHub, credentials or real binaries.
set -euo pipefail
scripts=$(cd "$(dirname "$0")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
git init -q -b main "$fixture/official"
git -C "$fixture/official" config user.name 'Release test'
git -C "$fixture/official" config user.email 'release-test@example.invalid'
printf 'official\n' > "$fixture/official/source.txt"
git -C "$fixture/official" add source.txt
git -C "$fixture/official" commit -qm 'official change'
git -C "$fixture/official" tag v1.2.3
git clone -q "$fixture/official" "$fixture/source"
git -C "$fixture/source" config user.name 'Release test'
git -C "$fixture/source" config user.email 'release-test@example.invalid'
git -C "$fixture/source" tag va.1.2.3-vb.0.0.0
printf 'fork\n' >> "$fixture/source/source.txt"
git -C "$fixture/source" commit -qam 'fork change'
main_sha=$(git -C "$fixture/source" rev-parse HEAD)
git -C "$fixture/source" tag va.1.2.3-vb.0.0.1
# A forged upstream tag in the fork must not establish the official baseline.
git -C "$fixture/source" tag v1.2.4
git -C "$fixture/source" tag va.1.2.4-vb.0.0.1
git -C "$fixture/source" tag va.1.2.5-vb.0.0.1
git -C "$fixture/source" checkout -qb feature
printf 'feature\n' >> "$fixture/source/source.txt"
git -C "$fixture/source" commit -qam feature
git -C "$fixture/source" tag va.1.2.3-vb.0.0.2
git -C "$fixture/source" checkout -q main
printf 'next official\n' >> "$fixture/official/source.txt"
git -C "$fixture/official" commit -qam 'next official change'
git -C "$fixture/official" tag v1.2.4
git clone -q "$fixture/source" "$fixture/work"
# Redirect only the production URL, and prohibit all network transports.
git -C "$fixture/work" config "url.$fixture/official.insteadOf" https://github.com/yetone/magpie.git

metadata() {
  local expected=$1 ref=$2 version=$3 publish=$4 event=${5:-workflow_dispatch} event_ref=${6:-refs/heads/main}
  : > "$fixture/output"
  if (
    cd "$fixture/work"
    GIT_ALLOW_PROTOCOL=file GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
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
grep -Fx 'tag=' "$fixture/output" >/dev/null
metadata pass main '1.2.3-rc.1' false
grep -Fx 'version=1.2.3-rc.1' "$fixture/output" >/dev/null
grep -Fx 'prerelease=true' "$fixture/output" >/dev/null
metadata fail main '1.2.3+build.2' false
metadata pass main va.1.2.3-vb.0.0.1 false
grep -Fx 'version=1.2.3-vb.0.0.1' "$fixture/output" >/dev/null
grep -Fx 'tag=va.1.2.3-vb.0.0.1' "$fixture/output" >/dev/null
grep -Fx 'upstream_version=1.2.3' "$fixture/output" >/dev/null
grep -Fx 'fork_version=0.0.1' "$fixture/output" >/dev/null
grep -Fx 'prerelease=false' "$fixture/output" >/dev/null
metadata pass main va.1.2.3-vb.0.0.1 true
metadata pass refs/tags/va.1.2.3-vb.0.0.1 '' true
grep -Fx 'tag=va.1.2.3-vb.0.0.1' "$fixture/output" >/dev/null
metadata pass '' '' false push refs/tags/va.1.2.3-vb.0.0.1
grep -Fx 'publish=true' "$fixture/output" >/dev/null
grep -Fx 'prerelease=false' "$fixture/output" >/dev/null
metadata pass '' '' true workflow_dispatch refs/tags/va.1.2.3-vb.0.0.1
grep -Fx 'version=1.2.3-vb.0.0.1' "$fixture/output" >/dev/null
metadata fail '' '' false push refs/tags/va.1.2.3-vb.0.0.2
metadata fail $'main\npublish=true' 1.2.3 true
metadata fail --upload-pack 1.2.3 false
metadata fail main $'1.2.3\npublish=true' false
metadata fail main '1.2.3$(touch injected)' false
metadata fail main 'va.1.2.3-vb.0.0.1$(touch injected)' false
metadata fail main 'va.01.2.3-vb.0.0.1' false
metadata fail main 'va.1.2.3-vb.0.00.1' false
metadata fail main 'va.1.2.3-vb.0.0.1+build' false
metadata fail main 'va.1.2.3-vb.0.0.1-rc.1' false
metadata fail main '' true
metadata fail main 1.2.3 true
metadata fail main 1.2.3-vb.0.0.1 true
metadata fail refs/tags/v1.2.4 '' true
metadata fail main va.1.2.3-vb.0.0.2 true
grep -F 'tag does not identify the source commit' "$fixture/log" >/dev/null
metadata fail refs/tags/va.1.2.3-vb.0.0.2 '' true
grep -F 'release commit must be on main' "$fixture/log" >/dev/null
metadata fail main va.1.2.4-vb.0.0.1 true
grep -F 'declared official release must be an ancestor' "$fixture/log" >/dev/null
metadata fail main va.1.2.5-vb.0.0.1 true

(
  cd "$fixture/work"
  bash "$scripts/release-notes.sh" va.1.2.3-vb.0.0.1 "$main_sha" "$fixture/notes"
)
grep -Fx '# magpie fork va.1.2.3-vb.0.0.1' "$fixture/notes" >/dev/null
grep -F 'Binary/package version: `1.2.3-vb.0.0.1`' "$fixture/notes" >/dev/null
grep -F 'Official base: [`v1.2.3`]' "$fixture/notes" >/dev/null
grep -F 'fork change' "$fixture/notes" >/dev/null
if grep -F 'official change' "$fixture/notes" >/dev/null; then
  echo 'Release notes included changes before the previous fork release.' >&2; exit 1
fi

mkdir "$fixture/assets"
printf 'MIT license notice\n' > "$fixture/assets/THIRD-PARTY-NOTICES.txt"
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
[[ $(wc -l < "$fixture/assets/SHA256SUMS") == 16 ]]
printf 'unexpected\n' > "$fixture/assets/magpie-linux-amd64.new"
if bash "$scripts/release-assets.sh" "$fixture/assets" > "$fixture/log" 2>&1; then
  echo 'An unexpected release asset was accepted.' >&2; exit 1
fi
rm "$fixture/assets/magpie-linux-amd64.new"
: > "$fixture/assets/magpie-cli-android-arm64"
if bash "$scripts/release-assets.sh" "$fixture/assets" > "$fixture/log" 2>&1; then
  echo 'An empty release asset was accepted.' >&2; exit 1
fi

# Latest moves only forward, by numeric version, from an upstream or fork tag.
(
  source "$scripts/release-version.sh"
  check_latest() {
    local got
    got=$(fork_release_latest "$1" "$2")
    [[ "$got" == "$3" ]] || { echo "Latest for $1 over '$2' was $got, want $3." >&2; exit 1; }
  }
  check_latest 0.1.1725-vb.0.0.8 '' true
  check_latest 0.1.1725-vb.0.0.8 va.0.1.1725-vb.0.0.6 true
  check_latest 0.1.1725-vb.0.0.10 va.0.1.1725-vb.0.0.9 true
  check_latest 0.1.1725-vb.0.0.6 va.0.1.1725-vb.0.0.8 false
  check_latest 0.1.1725-vb.0.0.8 va.0.1.1726-vb.0.0.1 false
  check_latest 0.1.1726-vb.0.0.1 v0.1.1725 true
)
# A workflow step that decides with these functions loads them itself: in
# an if, a missing function only reads as false (va.0.1.1725-vb.0.0.8 was
# published without becoming Latest).
for workflow in release.yml promote-release.yml; do
  if ! awk '
    /^      - / { sourced = 0; step = $0 }
    /source build\/fork\/release-version\.sh/ { sourced = 1 }
    /(parse_fork_release|fork_release_latest)[ "]/ && !sourced { print FILENAME ": " step ": " $0; bad = 1 }
    END { exit bad }
  ' "$scripts/../../.github/workflows/$workflow" >&2; then
    echo "A release step uses the version functions without sourcing release-version.sh." >&2; exit 1
  fi
done
printf 'Release metadata and exact asset checks passed.\n'
