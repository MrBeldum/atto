#!/bin/sh
# Install or update atto from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/sebastianrcnt/atto/main/install.sh | sh
#
# ATTO_VERSION=v0.1.0 picks a release (default: the latest; "edge" is the
# unstable build of main);
# ATTO_INSTALL_DIR picks the directory (default: ~/.local/bin).
# Running it again installs the latest release over the old one.
set -eu

repo=sebastianrcnt/atto
dir=${ATTO_INSTALL_DIR:-$HOME/.local/bin}
version=${ATTO_VERSION:-latest}

fail() { echo "atto install: $*" >&2; exit 1; }

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s); on Windows use install.ps1" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac
asset=atto_${os}_${arch}

if [ -n "${ATTO_DOWNLOAD_BASE:-}" ]; then # a mirror, or a local test server
  base=$ATTO_DOWNLOAD_BASE
elif [ "$version" = latest ]; then
  base=https://github.com/$repo/releases/latest/download
else
  base=https://github.com/$repo/releases/download/$version
fi

if command -v curl >/dev/null 2>&1; then
  get() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  get() { wget -qO "$2" "$1"; }
else
  fail "needs curl or wget"
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "needs sha256sum or shasum to verify the download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset ($version)..."
get "$base/$asset" "$tmp/atto" || fail "download failed: $base/$asset"
get "$base/checksums.txt" "$tmp/checksums.txt" || fail "download failed: $base/checksums.txt"

want=$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no $asset"
[ "$(sha "$tmp/atto")" = "$want" ] || fail "checksum mismatch for $asset; not installing"

mkdir -p "$dir"
chmod 755 "$tmp/atto"
mv -f "$tmp/atto" "$dir/atto.new"
mv -f "$dir/atto.new" "$dir/atto"
echo "Installed $("$dir/atto" -version) to $dir/atto"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH, e.g.: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" ;;
esac
