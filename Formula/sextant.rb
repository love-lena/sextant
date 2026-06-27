# Sextant — a protocol + SDK for AI agents to collaborate over a bus.
#
# This formula installs the prebuilt release binaries (sextant, sextant-mcp,
# sextant-dash, sextant-tui, sextant-dispatch, sextant-violet, sextant-workflow)
# from the GitHub release tarballs; it does not compile from
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
  version "0.9.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.9.0/sextant_v0.9.0_darwin_arm64.tar.gz"
      sha256 "8755fbcfd8887888d8c6f9fcd320b72eeddeb600dcf2b4f82f5f638ee1656470" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.9.0/sextant_v0.9.0_darwin_amd64.tar.gz"
      sha256 "5bb5304e5036da92e1ad1d5ad420d868501bbcc691e7c4b317b67235dbf535af" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.9.0/sextant_v0.9.0_linux_arm64.tar.gz"
      sha256 "96be0841ba9c2772d6d47832ebfb477124d460ca86b347e49e21c4acd348b5e1" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.9.0/sextant_v0.9.0_linux_amd64.tar.gz"
      sha256 "cb4b99eebb8a7363a1b764f0d48e8170275376b161d226005cb6cbe0c2728c91" # linux_amd64
    end
  end

  def install
    # Each tarball unpacks to a single top-level dir (Homebrew cds into it) with
    # bin/{sextant,sextant-mcp,sextant-dash,sextant-tui,sextant-dispatch,sextant-violet,sextant-workflow}.
    # Install all seven binaries.
    bin.install "bin/sextant"
    bin.install "bin/sextant-mcp"
    bin.install "bin/sextant-dash"
    bin.install "bin/sextant-tui"
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
