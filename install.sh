#!/bin/sh
# Installs a prebuilt mybox binary from a GitHub release, so installation
# needs no Go toolchain:
#
#   curl -fsSL https://raw.githubusercontent.com/overworks/mybox-cli/0.x/install.sh | sh
#
# Options (pass after `sh -s --` when piping):
#   --version vX.Y.Z   install this release instead of the latest
#   --bin-dir DIR      install into DIR (default ~/.local/bin)
set -eu

REPO="overworks/mybox-cli"

version="${MYBOX_VERSION:-}"
bin_dir="${MYBOX_INSTALL_DIR:-$HOME/.local/bin}"

usage() {
  sed -n '2,10p' "$0" 2>/dev/null || true
  echo "usage: install.sh [--version vX.Y.Z] [--bin-dir DIR]"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version) shift; version="${1:?--version needs a tag}" ;;
    --bin-dir) shift; bin_dir="${1:?--bin-dir needs a path}" ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install.sh: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

fail() { echo "install.sh: $*" >&2; exit 1; }

# curl with wget as the fallback, since minimal images often have only one.
fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then wget -qO- "$1"
  else fail "neither curl nor wget is available"
  fi
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS: $(uname -s) — for Windows, download the .zip from https://github.com/$REPO/releases" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if [ -z "$version" ]; then
  # The releases/latest API answers without authentication; take tag_name
  # from the JSON with sed so jq is not required.
  version=$(fetch "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$version" ] || fail "could not determine the latest release; pass --version"
fi

archive="mybox_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $archive ..." >&2
fetch "$base/$archive" > "$tmp/$archive"
fetch "$base/checksums.txt" > "$tmp/checksums.txt"

# checksums.txt names entries as ./mybox_..., so verify from inside the
# directory with the matching line only.
(
  cd "$tmp"
  line=$(grep " \./$archive\$" checksums.txt) || fail "no checksum for $archive"
  if command -v sha256sum >/dev/null 2>&1; then
    echo "$line" | sha256sum -c - >/dev/null
  elif command -v shasum >/dev/null 2>&1; then
    echo "$line" | shasum -a 256 -c - >/dev/null
  else
    fail "neither sha256sum nor shasum is available to verify the download"
  fi
)

tar -xzf "$tmp/$archive" -C "$tmp" mybox
mkdir -p "$bin_dir"
install -m 0755 "$tmp/mybox" "$bin_dir/mybox"

echo "installed $("$bin_dir/mybox" version) to $bin_dir/mybox" >&2

case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo "note: $bin_dir is not on your PATH" >&2 ;;
esac
