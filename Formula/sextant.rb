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
  version "0.7.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.7.0/sextant_v0.7.0_darwin_arm64.tar.gz"
      sha256 "ddbebe353bcdcaa68fef55305decd002f39b9d33ca98965286a033dfce7f8e3e" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.7.0/sextant_v0.7.0_darwin_amd64.tar.gz"
      sha256 "224ef9b514f7375a371c1a86ed2282381be89bf15ebfb8c126b611f18436fde7" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.7.0/sextant_v0.7.0_linux_arm64.tar.gz"
      sha256 "41ae9d900e3ecddcca6338e5529fee5d7af89f962927b81e981643ff8fcc3cc6" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.7.0/sextant_v0.7.0_linux_amd64.tar.gz"
      sha256 "fc545f749c7a43d4fd5f4b6d7e0cd130d49e88203f04980bc1dcdf6ec4aab695" # linux_amd64
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
