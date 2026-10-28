#!/usr/bin/env bash
# Adapted from yetone/magpie-releases at d576def69ef01e62a7f7bb540f37a790eae6f36f.
# Regenerate the Homebrew tap files — Formula/magpie.rb and Casks/magpie-app.rb —
# from the SHA256SUMS of a published Release.
#
#   scripts/update-tap.sh va.0.1.1725-vb.0.0.1
#   TAP_OUT_DIR=/tmp/tap scripts/update-tap.sh va.0.1.1725-vb.0.0.1
#   scripts/update-tap.sh --self-check
#
# TAP_RELEASE_REPO overrides the Release repo (default zwenooo/magpie);
# the self-check uses it to pick the latest Release.
set -euo pipefail

REPO="${TAP_RELEASE_REPO:-zwenooo/magpie}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../build/fork/release-version.sh
source "$ROOT/build/fork/release-version.sh"
OUT="${TAP_OUT_DIR:-$ROOT}"
HOMEPAGE="https://github.com/zwenooo/magpie"
[[ "$REPO" == "zwenooo/magpie" ]] || { echo "only this fork is an allowed tap release source" >&2; exit 1; }
DESC="Pick the model each AI coding agent on your machine uses"

die() { echo "update-tap: $*" >&2; exit 1; }

REPO_URL="https://github.com/$REPO/releases/download"

release_url() { echo "$REPO_URL/$1"; }

# The checksum of one asset in a SHA256SUMS file, or a loud failure if the
# asset is not in it — a release that is missing an asset must not produce a
# formula with a blank sha256.
sha() {  # $1 = SHA256SUMS contents, $2 = asset name
  local s
  s="$(awk -v f="$2" '$2 == f { print $1 }' <<<"$1")"
  [[ "$s" =~ ^[0-9a-f]{64}$ ]] || die "$2 has no unique valid checksum in this release's SHA256SUMS"
  echo "$s"
}

latest_version() {
  curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    jq -er '.tag_name | select(type == "string" and length > 0)'
}

render() {
  parse_fork_release "$1" || die "invalid fork release tag: $1"
  local version="$release_version" tag="$release_tag"
  local cask_updates='  auto_updates true'
  # Only the initial published packages predate the GitHub updater. A newer
  # upstream base starts its fork counter again, while keeping that capability.
  case "$tag" in
    va.0.1.1725-vb.0.0.[0-5]) cask_updates='  # This release predates in-app updates; upgrade it through Homebrew.' ;;
  esac
  local sums
  if [ -n "${TAP_SUMS_FILE:-}" ]; then
    sums="$(cat "$TAP_SUMS_FILE")"
  else
    sums="$(curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors "$(release_url "$tag")/SHA256SUMS")" ||
      die "no SHA256SUMS in $tag of $REPO"
  fi

  # Every checksum is resolved before anything is written.
  local cli_darwin_arm64 cli_darwin_amd64 cli_linux_arm64 cli_linux_amd64
  local app_arm64 app_amd64
  cli_darwin_arm64="$(sha "$sums" magpie-cli-darwin-arm64)"
  cli_darwin_amd64="$(sha "$sums" magpie-cli-darwin-amd64)"
  cli_linux_arm64="$(sha "$sums" magpie-cli-linux-arm64)"
  cli_linux_amd64="$(sha "$sums" magpie-cli-linux-amd64)"
  app_arm64="$(sha "$sums" magpie-darwin-arm64.zip)"
  app_amd64="$(sha "$sums" magpie-darwin-amd64.zip)"

  mkdir -p "$OUT/Formula" "$OUT/Casks"

  cat > "$OUT/Formula/magpie.rb" <<RB
class Magpie < Formula
  desc "$DESC"
  homepage "$HOMEPAGE"
  version "$version"
  license "MIT"

  on_macos do
    on_arm do
      url "$REPO_URL/$tag/magpie-cli-darwin-arm64"
      sha256 "$cli_darwin_arm64"
    end
    on_intel do
      url "$REPO_URL/$tag/magpie-cli-darwin-amd64"
      sha256 "$cli_darwin_amd64"
    end
  end
  on_linux do
    on_arm do
      url "$REPO_URL/$tag/magpie-cli-linux-arm64"
      sha256 "$cli_linux_arm64"
    end
    on_intel do
      url "$REPO_URL/$tag/magpie-cli-linux-amd64"
      sha256 "$cli_linux_amd64"
    end
  end

  def install
    bin.install Dir["magpie-cli-*"].first => "magpie"
  end

  test do
    assert_match "magpie #{version}", shell_output("#{bin}/magpie --version")
  end
end
RB

  cat > "$OUT/Casks/magpie-app.rb" <<RB
cask "magpie-app" do
  arch arm: "arm64", intel: "amd64"

  version "$version"
  sha256 arm:   "$app_arm64",
         intel: "$app_amd64"

  url "$REPO_URL/$tag/magpie-darwin-#{arch}.zip"
  name "magpie"
  desc "$DESC"
  homepage "$HOMEPAGE"

$cask_updates
  depends_on :macos

  app "magpie.app"
end
RB

  for file in "$OUT/Formula/magpie.rb" "$OUT/Casks/magpie-app.rb"; do
    if command -v ruby > /dev/null; then
      ruby -c "$file" > /dev/null || die "$file is not valid Ruby"
    fi
    echo "wrote $file"
  done
}

# A checksum has to sit with the asset it belongs to, not merely appear
# somewhere in the file, or a swapped arm64/amd64 pair would pass.
assert_next() {  # $1 = file, $2 = anchor, $3 = checksum expected on the next line
  grep -A1 "$2" "$1" | grep -q "$3" || die "$2 in $1 is not paired with its checksum"
}
assert_same() {  # $1 = file, $2 = anchor, $3 = checksum expected on the same line
  grep -q "$2.*$3" "$1" || die "$2 in $1 is not paired with its checksum"
}

self_check() (
  local tag version dir sums
  tag="$(latest_version)" || die "cannot determine the latest Release of $REPO"
  parse_fork_release "$tag" || die "latest Release is not a valid fork tag: $tag"
  version="$release_version"
  echo "self-check against $tag of $REPO"

  dir="$(mktemp -d)"
  trap 'rm -rf "${dir:-}"' EXIT
  OUT="$dir" render "$tag"

  local formula="$dir/Formula/magpie.rb" cask="$dir/Casks/magpie-app.rb"
  grep -Fq "/$tag/" "$formula" || die "formula is not at $tag"
  grep -Fq "/$tag/" "$cask" || die "cask is not at $tag"
  grep -Fq "version \"$version\"" "$formula" || die "formula version is not $version"
  grep -Fq "version \"$version\"" "$cask" || die "cask version is not $version"

  sums="$(curl -fsSL "$(release_url "$tag")/SHA256SUMS")"
  assert_next "$formula" 'magpie-cli-darwin-arm64"' "$(sha "$sums" magpie-cli-darwin-arm64)"
  assert_next "$formula" 'magpie-cli-darwin-amd64"' "$(sha "$sums" magpie-cli-darwin-amd64)"
  assert_next "$formula" 'magpie-cli-linux-arm64"'  "$(sha "$sums" magpie-cli-linux-arm64)"
  assert_next "$formula" 'magpie-cli-linux-amd64"'  "$(sha "$sums" magpie-cli-linux-amd64)"
  assert_same "$cask" 'arm:'   "$(sha "$sums" magpie-darwin-arm64.zip)"
  assert_same "$cask" 'intel:' "$(sha "$sums" magpie-darwin-amd64.zip)"

  # An asset missing from SHA256SUMS must fail the render, in a subshell so the
  # exit only ends the probe.
  if (sha "" magpie-cli-darwin-arm64) 2> /dev/null; then
    die "a missing asset did not fail the render"
  fi

  echo "self-check passed"
)

case "${1:-}" in
  --self-check) self_check ;;
  "" | -h | --help) die "usage: update-tap.sh <va.X.Y.Z-vb.X.Y.Z>|--self-check" ;;
  *) render "$1" ;;
esac
