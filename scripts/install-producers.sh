#!/bin/sh
# Install Ember producers from a GitHub release on a machine without Ember.app
# (Linux, or a Mac where the CLI should own the background services).
#
#   curl -fsSL https://raw.githubusercontent.com/tarakanof/Ember/main/scripts/install-producers.sh | sh -s -- [options]
#
# Options:
#   --version vX.Y.Z      release to install (default: latest)
#   --producers "a b"     which producers to set up: claude codex t3 (default: claude)
#   --bin-dir DIR         where binaries go (default: ~/.local/bin)
#   --no-install          only download, verify and copy the binaries
#   --force               proceed on a Mac that has Ember.app installed
#
# Steps: detect OS/arch, download ember-producers_<os>_<arch>.tar.gz and
# SHA256SUMS, verify the checksum, copy the binaries, run
# `<producer> install --headless` for each selected producer.
# EMBER_RELEASE_BASE_URL overrides the download base (tests, mirrors).
# Run it as your own user, not root: it installs per-user services.
#
# Everything runs inside main(), called on the last line, so a truncated
# download (curl | sh) defines functions at most and never runs a fragment.
set -eu

die() { echo "install-producers: $*" >&2; exit 1; }

usage() {
  cat <<'USAGE'
usage: install-producers.sh [--version vX.Y.Z] [--producers "claude codex t3"]
                            [--bin-dir DIR] [--no-install] [--force]
USAGE
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    die "need curl or wget"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    die "need sha256sum or shasum to verify the download"
  fi
}

main() {
  REPO=tarakanof/Ember
  version=latest
  producers=claude
  bin_dir="$HOME/.local/bin"
  run_install=1
  force=0

  if [ "$(id -u)" = 0 ]; then
    die "run this as the user who runs Claude Code / Codex, not root: producers install per-user services (systemd --user / LaunchAgents) under \$HOME"
  fi

  while [ $# -gt 0 ]; do
    case "$1" in
      --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
      --producers) [ $# -ge 2 ] || die "--producers needs a value"; producers=$2; shift 2 ;;
      --bin-dir) [ $# -ge 2 ] || die "--bin-dir needs a value"; bin_dir=$2; shift 2 ;;
      --no-install) run_install=0; shift ;;
      --force) force=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) usage >&2; die "unknown option: $1" ;;
    esac
  done

  for p in $producers; do
    case "$p" in
      claude|codex|t3) ;;
      *) die "unknown producer '$p' (want claude, codex or t3)" ;;
    esac
  done

  os=$(uname -s)
  arch=$(uname -m)
  case "$os" in
    Linux) os=linux
      case "$arch" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) die "unsupported Linux architecture: $arch (release builds: amd64, arm64)" ;;
      esac ;;
    Darwin) os=darwin; arch=universal
      if [ "$force" = 0 ] && { [ -d /Applications/Ember.app ] || [ -d "$HOME/Applications/Ember.app" ]; }; then
        die "Ember.app is installed and runs the producers itself (Settings › Agents). Use --force to install the CLI copies anyway."
      fi ;;
    *) die "unsupported OS: $os" ;;
  esac

  asset="ember-producers_${os}_${arch}.tar.gz"
  if [ -n "${EMBER_RELEASE_BASE_URL:-}" ]; then
    base=$EMBER_RELEASE_BASE_URL
  elif [ "$version" = latest ]; then
    base="https://github.com/$REPO/releases/latest/download"
  else
    base="https://github.com/$REPO/releases/download/$version"
  fi

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM

  echo "Downloading $asset ($version)..."
  fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
  fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $base/SHA256SUMS"

  want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
  [ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
  got=$(sha256 "$tmp/$asset")
  [ "$got" = "$want" ] || die "checksum mismatch for $asset (got $got, want $want)"
  echo "Checksum OK."

  tar -xzf "$tmp/$asset" -C "$tmp"
  src="$tmp/${asset%.tar.gz}"
  mkdir -p "$bin_dir"
  for p in claude codex t3; do
    b="ember-$p-producer"
    [ -f "$src/$b" ] || die "$b missing from $asset"
    cp "$src/$b" "$bin_dir/$b.tmp.$$"
    chmod 0755 "$bin_dir/$b.tmp.$$"
    mv -f "$bin_dir/$b.tmp.$$" "$bin_dir/$b"
  done
  echo "Installed ember-{claude,codex,t3}-producer $(cat "$src/VERSION" 2>/dev/null || echo "$version") to $bin_dir"

  case ":$PATH:" in
    *":$bin_dir:"*) ;;
    *) echo "Note: $bin_dir is not on PATH; add it so Claude Code's plugin hooks and your shell find the producers." ;;
  esac

  [ "$run_install" = 1 ] || exit 0
  for p in $producers; do
    echo
    echo "== ember-$p-producer install --headless"
    "$bin_dir/ember-$p-producer" install --headless
  done
  echo
  echo "Next: put the server's bearer token in ~/.config/ember/producer.env (EMBER_TOKEN=...),"
  echo "then check with: ember-<producer>-producer doctor"
}

main "$@"
