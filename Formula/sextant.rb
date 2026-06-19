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
  version "0.5.3"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.3/sextant_v0.5.3_darwin_arm64.tar.gz"
      sha256 "650eb162963422ef9b403202592cc7c29352b190f785c10c3b72953d57c8ecd8" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.3/sextant_v0.5.3_darwin_amd64.tar.gz"
      sha256 "3bc11cff5b03f7e1a76c4095d87e682d20a195083c501966db58ccc4bb5de7b3" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.3/sextant_v0.5.3_linux_arm64.tar.gz"
      sha256 "864078d7e9ae4613681a6c3c7591254b295b9173ff30daf0bdd034cad6e2aeef" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.3/sextant_v0.5.3_linux_amd64.tar.gz"
      sha256 "69a0dc1133e636c58ca3f5dff01a57b4af53caa626a2e1fc767bd218d0c3a07f" # linux_amd64
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
