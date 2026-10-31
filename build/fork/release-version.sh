#!/usr/bin/env bash
# Public fork tags keep both revisions; native binaries require a numeric prefix.
parse_fork_release() {
  release_tag= release_version= upstream_version= fork_version=
  local upstream fork component
  local -a components
  [[ "${1:-}" =~ ^va\.([0-9]+\.[0-9]+\.[0-9]+)-vb\.([0-9]+\.[0-9]+\.[0-9]+)$ ]] || return 1
  upstream=${BASH_REMATCH[1]}
  fork=${BASH_REMATCH[2]}
  IFS=. read -r -a components <<< "$upstream.$fork"
  for component in "${components[@]}"; do
    [[ "$component" == 0 || "$component" == [1-9]* ]] || return 1
  done
  release_tag=$1
  release_version=$upstream-vb.$fork
  upstream_version=$upstream
  fork_version=$fork
}

# Prints true when a stable release of VERSION should become Latest over
# LATEST_TAG (the current Latest's tag, empty when none): publishing an older
# version must not demote a newer one. Run it in a command substitution, so
# a step that didn't source this file fails instead of deciding false.
fork_release_latest() {
  local version=$1 latest_tag=$2 latest_version
  [[ -n "$latest_tag" ]] || { echo true; return; }
  latest_version=${latest_tag#v}
  if parse_fork_release "$latest_tag"; then latest_version=$release_version; fi
  if [[ $(printf '%s\n%s\n' "$version" "$latest_version" | sort -V | tail -1) == "$version" ]]; then
    echo true
  else
    echo false
  fi
}
