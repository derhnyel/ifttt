#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

release_version="${RELEASE_VERSION-dev}"
release_commit="${RELEASE_COMMIT-$(git -C "$ROOT" rev-parse --verify HEAD 2>/dev/null || printf unknown)}"
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
if [[ "$release_version" != dev && ! "$release_version" =~ $version_pattern ]]; then
  printf 'Invalid RELEASE_VERSION: expected dev or vMAJOR.MINOR.PATCH[-prerelease]\n' >&2
  exit 2
fi
if [[ "$release_commit" != unknown && ! "$release_commit" =~ ^[0-9a-fA-F]{40}$ ]]; then
  printf 'Invalid RELEASE_COMMIT: expected unknown or a 40-character hexadecimal commit\n' >&2
  exit 2
fi

mkdir -p "$ROOT/build"
stage="$(mktemp -d "$ROOT/build/.release.XXXXXXXX")"
backup=''
cleanup() {
  local status=$?
  if [[ -n "$stage" ]]; then rm -rf -- "$stage"; fi
  if [[ -n "$backup" ]]; then
    if [[ ! -e "$ROOT/build/release" && ! -L "$ROOT/build/release" ]]; then
      mv -- "$backup/release" "$ROOT/build/release"
    fi
    rm -rf -- "$backup"
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

assets=()
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  IFS=/ read -r target_os target_arch <<< "$platform"
  suffix=''
  if [ "$target_os" = windows ]; then suffix='.exe'; fi
  asset="iflint-$target_os-$target_arch$suffix"
  assets+=("$asset")
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath \
    -ldflags "-X main.version=$release_version -X main.commit=$release_commit" \
    -o "$stage/$asset" ./cmd)
done
(cd "$stage" && if command -v sha256sum >/dev/null; then
  sha256sum "${assets[@]}" > SHA256SUMS
else
  shasum -a 256 "${assets[@]}" > SHA256SUMS
fi)

if [[ -e "$ROOT/build/release" || -L "$ROOT/build/release" ]]; then
  backup="$(mktemp -d "$ROOT/build/.release-backup.XXXXXXXX")"
  mv -- "$ROOT/build/release" "$backup/release"
fi
mv -- "$stage" "$ROOT/build/release"
stage=''
printf 'Release assets: %s/build/release\n' "$ROOT"
