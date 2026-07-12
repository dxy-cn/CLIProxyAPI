#!/usr/bin/env bash

set -euo pipefail

release_tag="${1:-}"
if [[ ! "$release_tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)-dxy\.([1-9][0-9]*)$ ]]; then
  echo "invalid release tag: $release_tag" >&2
  echo "expected format: v<upstream-version>-dxy.<revision>, for example v7.2.70-dxy.1" >&2
  exit 1
fi

upstream_tag="v${BASH_REMATCH[1]}"
dxy_revision="${BASH_REMATCH[2]}"

if ! git rev-parse --verify --quiet "refs/tags/${upstream_tag}^{commit}" >/dev/null; then
  echo "upstream tag does not exist: $upstream_tag" >&2
  exit 1
fi
if ! git rev-parse --verify --quiet "refs/tags/${release_tag}^{commit}" >/dev/null; then
  echo "release tag does not exist: $release_tag" >&2
  exit 1
fi

release_commit="$(git rev-parse "refs/tags/${release_tag}^{commit}")"
head_commit="$(git rev-parse HEAD)"
if [[ "$release_commit" != "$head_commit" ]]; then
  echo "release tag $release_tag points to $release_commit, but checkout HEAD is $head_commit" >&2
  exit 1
fi

if ! git merge-base --is-ancestor "$upstream_tag" "$release_commit"; then
  echo "upstream tag $upstream_tag is not an ancestor of release $release_tag" >&2
  exit 1
fi

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    echo "release_tag=$release_tag"
    echo "upstream_tag=$upstream_tag"
    echo "dxy_revision=$dxy_revision"
    echo "release_commit=$release_commit"
  } >> "$GITHUB_OUTPUT"
fi

echo "validated release=$release_tag upstream=$upstream_tag revision=$dxy_revision commit=$release_commit"
