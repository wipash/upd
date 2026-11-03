#!/bin/sh
# Installs the latest upd release to ~/.local/bin (or $UPD_INSTALL_DIR).
set -eu

repo=wipash/upd
dir=${UPD_INSTALL_DIR:-$HOME/.local/bin}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "upd: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "upd: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
asset="upd_${os}_${arch}"
base="https://github.com/$repo/releases/latest/download"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

want=$(awk -v a="$asset" '$2 == a { print $1 }' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "upd: checksum mismatch for $asset" >&2
  exit 1
fi

mkdir -p "$dir"
chmod 755 "$tmp/$asset"
mv "$tmp/$asset" "$dir/upd"
echo "installed $dir/upd ($("$dir/upd" version))"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "note: $dir is not on your PATH" ;;
esac
