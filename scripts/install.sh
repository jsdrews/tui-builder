#!/bin/sh
# Install tui-builder and wrangl from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/jsdrews/tui-builder/main/scripts/install.sh | sh
#
# Environment:
#   VERSION      release tag to install, e.g. v0.5.0 (default: latest release)
#   INSTALL_DIR  where the binaries go (default: ~/.local/bin)
set -eu

REPO=jsdrews/tui-builder
INSTALL_DIR=${INSTALL_DIR:-$HOME/.local/bin}

die() { echo "install.sh: $*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS $(uname -s); download a release by hand from https://github.com/$REPO/releases" ;;
esac

case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

# /releases/latest redirects to the newest non-prerelease tag.
tag=${VERSION:-}
if [ -z "$tag" ]; then
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
  tag=${tag##*/}
fi
case $tag in
  v*) ;;
  *) die "could not work out the latest release (got '$tag'); set VERSION=vX.Y.Z" ;;
esac

archive="tui-builder_${tag#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $archive ($tag)"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "download failed: $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "download failed: $base/checksums.txt"

want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || die "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
[ "$want" = "$got" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$INSTALL_DIR"
for bin in tui-builder wrangl; do
  install -m 755 "$tmp/$bin" "$INSTALL_DIR/$bin"
done

echo "Installed tui-builder and wrangl $tag to $INSTALL_DIR"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Note: $INSTALL_DIR is not on your PATH. Add it, e.g. export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
