#!/usr/bin/env bash
# Regenerate Formula/sextant.rb for a release tag from the just-built dist/
# tarballs (TASK-59). The release workflow runs this after scripts/release.sh,
# commits the result on a branch, and opens an auto-merge PR to main.
#
#   scripts/gen-formula.sh v0.1.2
#
# Output (stdout, also written to Formula/sextant.rb): a Homebrew formula that
# installs the four prebuilt platform tarballs with their fresh sha256s. The
# template below (a quoted heredoc, so no shell expansion mangles Ruby's
# #{...} or backticks) is the source of truth for the formula's shape — the
# hand-written Formula/sextant.rb must match it byte-for-byte so the first
# automated bump is a clean diff.
set -euo pipefail
cd "$(dirname "$0")/.."

tag="${1:?usage: scripts/gen-formula.sh <tag>}"   # e.g. v0.1.2
ver="${tag#v}"                                     # e.g. 0.1.2

# sha256 of a dist tarball for a platform, failing loudly if it is missing.
sha() {
  local f="dist/sextant_${tag}_$1.tar.gz"
  [ -f "$f" ] || { echo "gen-formula: missing $f (run scripts/release.sh $tag first)" >&2; exit 1; }
  shasum -a 256 "$f" | awk '{print $1}'
}

darwin_arm64="$(sha darwin_arm64)"
darwin_amd64="$(sha darwin_amd64)"
linux_arm64="$(sha linux_arm64)"
linux_amd64="$(sha linux_amd64)"

# Quoted heredoc: emitted verbatim, placeholders substituted in the pipe below.
template() {
  cat <<'TEMPLATE'
# Sextant — a protocol + SDK for AI agents to collaborate over a bus.
#
# This formula installs the prebuilt release binaries (sextant, sextant-mcp,
# sextant-dash, sextant-dispatch, sextant-violet, sextant-workflow) from the
# GitHub release tarballs; it does not compile from
# source. The version + the four per-platform sha256 lines below are
# regenerated automatically by .github/workflows/release.yml on each v* tag
# (see scripts/gen-formula.sh) — keep their shape stable so the bump diff is
# clean.
#
# Install:
#   brew tap love-lena/sextant https://github.com/love-lena/sextant
#   brew install sextant
# Run the bus as a managed daemon:
#   brew services start sextant
class Sextant < Formula
  desc "Protocol + SDK + CLI for AI agents to collaborate over a bus"
  homepage "https://github.com/love-lena/sextant"
  # No LICENSE file ships in the repo yet, so `license` is intentionally
  # omitted (TODO: add once the repo declares one).
  version "@VER@"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/@TAG@/sextant_@TAG@_darwin_arm64.tar.gz"
      sha256 "@SHA_DARWIN_ARM64@" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/@TAG@/sextant_@TAG@_darwin_amd64.tar.gz"
      sha256 "@SHA_DARWIN_AMD64@" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/@TAG@/sextant_@TAG@_linux_arm64.tar.gz"
      sha256 "@SHA_LINUX_ARM64@" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/@TAG@/sextant_@TAG@_linux_amd64.tar.gz"
      sha256 "@SHA_LINUX_AMD64@" # linux_amd64
    end
  end

  def install
    # Each tarball unpacks to a single top-level dir (Homebrew cds into it) with
    # bin/{sextant,sextant-mcp,sextant-dash,sextant-dispatch,sextant-violet,sextant-workflow}.
    # Install all six binaries.
    bin.install "bin/sextant"
    bin.install "bin/sextant-mcp"
    bin.install "bin/sextant-dash"
    bin.install "bin/sextant-dispatch"
    bin.install "bin/sextant-violet"
    bin.install "bin/sextant-workflow"
  end

  # The bus is a per-user daemon. Run it with NO --store override so it uses
  # sextant's default per-user store (UserConfigDir/sextant/jetstream) — the
  # SAME store the bare CLI and the plugin's MCP discover. Pinning a Homebrew
  # var dir here made `sextant dash` (and the plugin) look in the wrong place
  # and report "no servers" out of the box (TASK-60). A user LaunchAgent has
  # $HOME, so the default store resolves.
  service do
    run [opt_bin/"sextant", "up"]
    keep_alive true
    log_path var/"log/sextant.log"
    error_log_path var/"log/sextant.log"
  end

  test do
    assert_match "sextant v#{version}", shell_output("#{bin}/sextant version")
  end
end
TEMPLATE
}

mkdir -p Formula
template \
  | sed -e "s|@VER@|${ver}|g" \
        -e "s|@TAG@|${tag}|g" \
        -e "s|@SHA_DARWIN_ARM64@|${darwin_arm64}|g" \
        -e "s|@SHA_DARWIN_AMD64@|${darwin_amd64}|g" \
        -e "s|@SHA_LINUX_ARM64@|${linux_arm64}|g" \
        -e "s|@SHA_LINUX_AMD64@|${linux_amd64}|g" \
  | tee Formula/sextant.rb
