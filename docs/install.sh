#!/bin/sh
# sabr one-line installer (macOS, Linux, FreeBSD, WSL):
#   curl -fsSL https://mrzroot.github.io/sabr/install.sh | sh
# Downloads the right binary from the latest GitHub release, resumes if the connection drops,
# verifies its SHA-256 and installs it to ~/.local/bin (or $SABR_BIN_DIR). No root needed.
# Options: SABR_VERSION=v0.1.0  SABR_BIN_DIR=/usr/local/bin  SABR_BASE_URL=<mirror of the release assets>
set -eu

say() { printf '%s\n' "$*" >&2; }
die() { say "sabr install: $*"; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux) os=linux ;; darwin) os=darwin ;; freebsd) os=freebsd ;;
  msys*|mingw*|cygwin*) die "on Windows use PowerShell:  irm https://mrzroot.github.io/sabr/install.ps1 | iex" ;;
  *) die "unsupported OS $os" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; armv7*|armv6*|armhf) arch=arm ;; i?86) arch=386 ;;
  *) die "unsupported CPU $arch" ;;
esac
asset="sabr_${os}_${arch}"

if [ -n "${SABR_BASE_URL:-}" ]; then base="$SABR_BASE_URL"
elif [ -n "${SABR_VERSION:-}" ]; then base="https://github.com/mrzroot/sabr/releases/download/$SABR_VERSION"
else base="https://github.com/mrzroot/sabr/releases/latest/download"; fi

bindir="${SABR_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$bindir" || die "cannot create $bindir"
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t sabr)
trap 'rm -rf "$tmp"' EXIT INT TERM

# fetch URL FILE: resumable, retried; this installer practises what sabr preaches
fetch() {
  i=0
  while :; do
    i=$((i + 1))
    if command -v curl >/dev/null 2>&1; then
      curl -fL --retry 3 --connect-timeout 20 -sS -C - -o "$2" "$1" && return 0
    elif command -v wget >/dev/null 2>&1; then
      wget -q -c -O "$2" "$1" && return 0
    else
      die "need curl or wget"
    fi
    [ "$i" -ge 15 ] && return 1
    say "  connection dropped; resuming ($i)..."
    sleep 2
  done
}

say "==> Downloading $asset"
fetch "$base/$asset" "$tmp/sabr" || die "download failed: $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download SHA256SUMS"
want=$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
[ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/sabr" | cut -d' ' -f1)
else got=$(shasum -a 256 "$tmp/sabr" | cut -d' ' -f1); fi
[ "$want" = "$got" ] || die "checksum mismatch (expected $want, got $got)"
say "  checksum ok"
chmod +x "$tmp/sabr"
mv -f "$tmp/sabr" "$bindir/sabr"
say "==> Installed $("$bindir/sabr" version) to $bindir/sabr"

case ":$PATH:" in
  *":$bindir:"*) ;;
  *)
    line="export PATH=\"$bindir:\$PATH\""
    for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
      if [ -f "$rc" ] || [ "$rc" = "$HOME/.profile" ]; then
        grep -qs "$bindir" "$rc" || printf '\n# added by the sabr installer\n%s\n' "$line" >> "$rc"
      fi
    done
    say "  added $bindir to PATH in your shell profile; open a new terminal or run:  $line"
    ;;
esac
say ""
say "Try:  sabr clone https://github.com/torvalds/linux"
say "      sabr docker pull python:3.12-slim --load"
