#!/bin/sh
# Install the workly binary from GitHub releases:
#
#   curl -fsSL https://raw.githubusercontent.com/worklydev/workly/main/install.sh | sh
#
# WORKLY_VERSION pins a release (e.g. v0.1.0; default latest).
# WORKLY_INSTALL_DIR sets where to put it (default ~/.local/bin).
# On Windows, download the zip from the releases page instead.
set -eu

repo="worklydev/workly"
version="${WORKLY_VERSION:-latest}"
dir="${WORKLY_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "workly: unsupported OS $(uname -s); see https://github.com/$repo/releases" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "workly: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  base="https://github.com/$repo/releases/download/$version"
fi
base="${WORKLY_DOWNLOAD_URL:-$base}" # for testing against a local build
archive="workly_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $archive ($version)..."
curl -fsSL -o "$tmp/$archive" "$base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

want="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null; then
  got="$(sha256sum "$tmp/$archive" | cut -d' ' -f1)"
else
  got="$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)"
fi
if [ -z "$want" ] || [ "$got" != "$want" ]; then
  echo "workly: checksum mismatch for $archive" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" workly
mkdir -p "$dir"
install -m 755 "$tmp/workly" "$dir/workly"
echo "Installed $("$dir/workly" version | head -1) to $dir/workly"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH to run it as \`workly\`." ;;
esac
echo "Get started: workly dev"
