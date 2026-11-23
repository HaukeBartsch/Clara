#!/usr/bin/env bash
# Builds a CLARA installation kit for an x86_64 RHEL 9 server (INSTALL.md §3).
#
#   tools/build_release.sh              # run the API tests, then build the kit
#   tools/build_release.sh --skip-tests # build without running go vet / go test
#
# Everything is built from the committed HEAD (via git archive), never from the working tree,
# so the kit is exactly the commit its name carries; uncommitted changes are not included.
#
# Output in dist/:
#   clara-<rev>-rhel-amd64-kit.tar.gz
#     clara-<rev>-rhel-amd64-kit/
#       clara-<rev>.tar.gz     the release: clara/bin/api-server, clara/web/, clara/.env.example
#       deploy/                reverse-proxy reference configurations (nginx, apache2)
#       INSTALL.md             installation guide
#       SHA256SUMS             checksums of the files above; on the server:
#                                cd clara-<rev>-rhel-amd64-kit && sha256sum -c SHA256SUMS
#
# The release tarball has the layout INSTALL.md §4 unpacks into /opt. The assistant model
# (tools/fetch_assistant_model.sh) is not part of the kit.

set -euo pipefail

GOOS_TARGET=linux
GOARCH_TARGET=amd64
PLATFORM=rhel-amd64

run_tests=1
case "${1:-}" in
  "") ;;
  --skip-tests) run_tests=0 ;;
  -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  *) echo "unknown option: $1 (use --skip-tests or --help)" >&2; exit 2 ;;
esac

repo_root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
cd "$repo_root"

for cmd in git go file; do
  command -v "$cmd" >/dev/null || { echo "missing required tool: $cmd" >&2; exit 1; }
done

if command -v sha256sum >/dev/null; then
  sha256() { sha256sum "$@"; }
elif command -v shasum >/dev/null; then
  sha256() { shasum -a 256 "$@"; }
else
  echo "missing required tool: sha256sum or shasum" >&2; exit 1
fi

# bsdtar (macOS) adds ._* AppleDouble files and extended attributes unless told not to;
# GNU tar knows neither option.
if tar --version 2>/dev/null | grep -q bsdtar; then
  make_tgz() { COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata --uid 0 --gid 0 -czf "$@"; }
else
  make_tgz() { tar --owner=0 --group=0 --numeric-owner -czf "$@"; }
fi

rev=$(git rev-parse --short HEAD)
release_name="clara-${rev}"
kit_name="${release_name}-${PLATFORM}-kit"

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "note: the working tree has uncommitted changes; they are NOT in the kit (building ${rev})" >&2
fi

dist="$repo_root/dist"
work=$(mktemp -d "${TMPDIR:-/tmp}/clara-release.XXXXXX")
trap 'rm -rf "$work"' EXIT

src="$work/src"
stage="$work/stage"
kit="$dist/$kit_name"
mkdir -p "$src" "$stage/clara/bin"

echo "==> exporting ${rev}"
# the whole tree: the API tests read fixtures outside api/ (tools/, assets/)
git archive --format=tar HEAD | tar -x -C "$src"

if [ "$run_tests" -eq 1 ]; then
  echo "==> go vet / go test"
  (cd "$src/api" && go vet ./... && go test ./...)
fi

echo "==> building api-server for ${GOOS_TARGET}/${GOARCH_TARGET}"
(cd "$src/api" && CGO_ENABLED=0 GOOS=$GOOS_TARGET GOARCH=$GOARCH_TARGET \
  go build -trimpath -ldflags="-s -w" -o "$stage/clara/bin/api-server" ./cmd/server)

info=$(file -b "$stage/clara/bin/api-server")
case "$info" in
  *ELF*x86-64*statically\ linked*) ;;
  *) echo "unexpected binary: $info" >&2; exit 1 ;;
esac

cp -R "$src/web" "$src/.env.example" "$stage/clara/"
chmod 0755 "$stage/clara/bin/api-server"

echo "==> packaging ${kit_name}.tar.gz"
rm -rf "$kit" "$dist/$kit_name.tar.gz"
mkdir -p "$kit"
make_tgz "$kit/$release_name.tar.gz" -C "$stage" clara
cp -R "$src/deploy" "$src/INSTALL.md" "$kit/"

(cd "$kit" && find . -type f ! -name SHA256SUMS | sed 's|^\./||' | LC_ALL=C sort \
  | while IFS= read -r f; do sha256 "$f"; done > SHA256SUMS)

make_tgz "$dist/$kit_name.tar.gz" -C "$dist" "$kit_name"
rm -rf "$kit"

echo
echo "built: dist/$kit_name.tar.gz"
echo "  binary: $info"
echo
echo "on the server:"
echo "  tar -xzf $kit_name.tar.gz && cd $kit_name && sha256sum -c SHA256SUMS"
echo "  then INSTALL.md §4 with $release_name.tar.gz"
