#!/usr/bin/env bash
# Resolve a requested ref once. Every build and check consumes this immutable SHA.
set -euo pipefail

[[ "$GITHUB_REPOSITORY" == zwenooo/magpie ]] || { echo 'This release workflow belongs to zwenooo/magpie.' >&2; exit 1; }
requested=${INPUT_REF:-$GITHUB_SHA}
version=${INPUT_VERSION:-}
publish=false
tag=
if [[ -z "${INPUT_REF:-}" && "$GITHUB_REF" == refs/tags/v* ]]; then requested=$GITHUB_REF; fi
if [[ "$GITHUB_EVENT_NAME" == push && "$GITHUB_REF" == refs/tags/* ]]; then
  requested=$GITHUB_REF
  version=${GITHUB_REF#refs/tags/v}
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
if [[ -z "$version" ]]; then
  candidate=${requested#refs/tags/}
  if [[ "$candidate" == v[0-9]* ]]; then version=${candidate#v}; else version=fork-${sha:0:8}; fi
fi
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ && "$version" != fork-${sha:0:8} ]]; then
  echo 'Version must be X.Y.Z with an optional prerelease suffix (no build metadata).' >&2
  exit 1
fi
if [[ "$publish" == true ]]; then
  [[ "$version" != fork-* ]] || { echo 'Publishing requires a versioned tag.' >&2; exit 1; }
  tag=v$version
  git fetch --no-tags origin "refs/tags/$tag"
  [[ $(git rev-parse 'FETCH_HEAD^{commit}') == "$sha" ]] || { echo 'The release tag does not identify the source commit.' >&2; exit 1; }
  git fetch --no-tags origin refs/heads/main
  git merge-base --is-ancestor "$sha" FETCH_HEAD || { echo 'The release commit must be on main.' >&2; exit 1; }
fi
prerelease=false
[[ "$version" != *-* ]] || prerelease=true
{
  printf 'sha=%s\nversion=%s\ntag=%s\npublish=%s\nprerelease=%s\n' "$sha" "$version" "$tag" "$publish" "$prerelease"
} >> "$GITHUB_OUTPUT"
printf 'Source: %s\nVersion: %s\nPublish: %s\n' "$sha" "$version" "$publish"
