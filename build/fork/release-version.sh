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
