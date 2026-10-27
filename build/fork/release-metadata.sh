#!/usr/bin/env bash
# Resolve a requested ref once. Every build and check consumes this immutable SHA.
set -euo pipefail
scripts=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-version.sh
source "$scripts/release-version.sh"

[[ "$GITHUB_REPOSITORY" == zwenooo/magpie ]] || { echo 'This release workflow belongs to zwenooo/magpie.' >&2; exit 1; }
requested=${INPUT_REF:-$GITHUB_SHA}
requested_version=${INPUT_VERSION:-}
publish=false
tag=
if [[ -z "${INPUT_REF:-}" && "$GITHUB_REF" == refs/tags/v* ]]; then requested=$GITHUB_REF; fi
if [[ "$GITHUB_EVENT_NAME" == push && "$GITHUB_REF" == refs/tags/* ]]; then
  requested=$GITHUB_REF
  requested_version=${GITHUB_REF#refs/tags/}
  publish=true
elif [[ "$GITHUB_EVENT_NAME" == workflow_dispatch && "${INPUT_PUBLISH:-false}" == true ]]; then
  publish=true
fi

# Git ref syntax and this allowlist reject options, output injection, shell
# metacharacters and revision expressions; git is always passed a quoted ref.
[[ "$requested" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || { echo 'Invalid source ref.' >&2; exit 1; }
if [[ ! "$requested" =~ ^[0-9a-fA-F]{40}$ ]]; then
  if [[ "$requested" == refs/* ]]; then
    git check-ref-format "$requested"
  else
    git check-ref-format --branch "$requested" >/dev/null
  fi
fi
git fetch --no-tags origin "$requested"
sha=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || exit 1
if [[ "$GITHUB_EVENT_NAME" == push || -z "${INPUT_REF:-}" ]]; then
  [[ "$sha" == "$GITHUB_SHA" ]] || { echo 'The event ref moved before the build started.' >&2; exit 1; }
fi
if [[ -z "$requested_version" ]]; then
  candidate=${requested#refs/tags/}
  if [[ "$candidate" == va.* ]]; then
    requested_version=$candidate
  elif [[ "$candidate" == v[0-9]* ]]; then
    requested_version=${candidate#v}
  else
    requested_version=fork-${sha:0:8}
  fi
fi
prerelease=false
if parse_fork_release "$requested_version"; then
  version=$release_version
  tag=$release_tag
elif [[ "$requested_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ || "$requested_version" == fork-${sha:0:8} ]]; then
  version=$requested_version
  [[ "$version" != *-* ]] || prerelease=true
else
  echo 'Version must be va.X.Y.Z-vb.X.Y.Z; numeric versions are accepted for build-only runs.' >&2
  exit 1
fi
if [[ "$publish" == true ]]; then
  [[ -n "$tag" ]] || { echo 'Publishing requires a va.X.Y.Z-vb.X.Y.Z fork tag.' >&2; exit 1; }
  git fetch --no-tags origin "refs/tags/$tag"
  [[ $(git rev-parse 'FETCH_HEAD^{commit}') == "$sha" ]] || { echo 'The release tag does not identify the source commit.' >&2; exit 1; }
  git fetch --no-tags origin refs/heads/main
  git merge-base --is-ancestor "$sha" FETCH_HEAD || { echo 'The release commit must be on main.' >&2; exit 1; }
  # Read the official tag from its fixed repository, never from the fork's tags.
  git fetch --no-tags https://github.com/yetone/magpie.git "refs/tags/v$upstream_version"
  git merge-base --is-ancestor FETCH_HEAD "$sha" || { echo 'The declared official release must be an ancestor of the source commit.' >&2; exit 1; }
fi
{
  printf 'sha=%s\nversion=%s\ntag=%s\npublish=%s\nprerelease=%s\n' "$sha" "$version" "$tag" "$publish" "$prerelease"
  printf 'upstream_version=%s\nfork_version=%s\n' "$upstream_version" "$fork_version"
} >> "$GITHUB_OUTPUT"
printf 'Source: %s\nVersion: %s\nRelease tag: %s\nOfficial base: %s\nFork revision: %s\nPublish: %s\n' "$sha" "$version" "$tag" "$upstream_version" "$fork_version" "$publish"
