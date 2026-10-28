#!/usr/bin/env bash
# Exercise the real generator with offline checksums before publishing a tap.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
assets=(magpie-cli-darwin-arm64 magpie-cli-darwin-amd64 magpie-cli-linux-arm64 magpie-cli-linux-amd64 magpie-darwin-arm64.zip magpie-darwin-amd64.zip)
for i in "${!assets[@]}"; do printf '%064x  %s\n' "$((i + 1))" "${assets[$i]}"; done > "$work/sums"
tag=va.0.1.1725-vb.0.0.1
version=0.1.1725-vb.0.0.1
TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/out" bash "$root/scripts/update-tap.sh" "$tag"
for asset in "${assets[@]:0:4}"; do
  hash=$(awk -v name="$asset" '$2 == name {print $1}' "$work/sums")
  grep -F -A1 "/$tag/$asset\"" "$work/out/Formula/magpie.rb" | grep -Fq "$hash"
done
for file in "$work/out/Formula/magpie.rb" "$work/out/Casks/magpie-app.rb"; do
  grep -Fq "version \"$version\"" "$file"
  grep -Fq "https://github.com/zwenooo/magpie/releases/download/$tag/" "$file"
done
! grep -Fq 'auto_updates true' "$work/out/Casks/magpie-app.rb"
for legacy in va.0.1.1725-vb.0.0.4 va.0.1.1725-vb.0.0.5; do
  TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/legacy" bash "$root/scripts/update-tap.sh" "$legacy"
  ! grep -Fq 'auto_updates true' "$work/legacy/Casks/magpie-app.rb"
done
for enabled in va.0.1.1725-vb.0.0.6 va.0.1.1725-vb.0.0.10 va.0.1.1726-vb.0.0.1; do
  TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/enabled" bash "$root/scripts/update-tap.sh" "$enabled"
  grep -Fq 'auto_updates true' "$work/enabled/Casks/magpie-app.rb"
done
for spec in 'arm:5' 'intel:6'; do
  hash=$(printf '%064x' "${spec#*:}")
  grep -E "${spec%:*}:.*$hash" "$work/out/Casks/magpie-app.rb" > /dev/null
done
head -n 5 "$work/sums" > "$work/missing"
cat "$work/sums" "$work/sums" > "$work/duplicate"
for bad in missing duplicate; do
  if TAP_SUMS_FILE="$work/$bad" TAP_OUT_DIR="$work/$bad-out" bash "$root/scripts/update-tap.sh" "$tag"; then
    echo "invalid checksum list accepted: $bad" >&2; exit 1
  fi
  test ! -e "$work/$bad-out"
done
for bad in 'bad/version' 'v0.1.1725' "$version" 'va.0.1.1725-vb.01.0.1'; do
  if TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/invalid" bash "$root/scripts/update-tap.sh" "$bad"; then
    echo "invalid fork release tag accepted: $bad" >&2; exit 1
  fi
  test ! -e "$work/invalid"
done
if TAP_RELEASE_REPO=yetone/magpie-releases TAP_SUMS_FILE="$work/sums" TAP_OUT_DIR="$work/upstream" bash "$root/scripts/update-tap.sh" "$tag"; then
  echo 'upstream release source accepted' >&2; exit 1
fi

# Exercise latest-release JSON parsing and exact download paths without a network.
mkdir "$work/bin"
cat > "$work/bin/curl" <<'SH'
#!/usr/bin/env bash
case "${!#}" in
  https://api.github.com/repos/zwenooo/magpie/releases/latest)
    cat "$TAP_TEST_JSON" ;;
  "https://github.com/zwenooo/magpie/releases/download/$TAP_TEST_TAG/SHA256SUMS")
    cat "$TAP_TEST_SUMS" ;;
  *) echo "unexpected download URL: ${!#}" >&2; exit 1 ;;
esac
SH
chmod +x "$work/bin/curl"
export PATH="$work/bin:$PATH" TAP_TEST_TAG="$tag" TAP_TEST_SUMS="$work/sums" TAP_TEST_JSON="$work/release.json"
for json in "{\"name\":\"Release\",\"tag_name\":\"$tag\"}" "{ \"tag_name\" : \"$tag\" }"; do
  printf '%s\n' "$json" > "$TAP_TEST_JSON"
  bash "$root/scripts/update-tap.sh" --self-check
done
for json in '{}' '{"tag_name":null}' '{"tag_name":42}' '{"tag_name":"v0.1.1725"}' '{broken'; do
  printf '%s\n' "$json" > "$TAP_TEST_JSON"
  if bash "$root/scripts/update-tap.sh" --self-check; then
    echo "invalid latest release metadata accepted: $json" >&2; exit 1
  fi
done
echo 'Homebrew generation checks passed'
