# Sextant — a protocol + SDK for AI agents to collaborate over a bus.
#
# This formula installs the prebuilt release binaries (sextant, sextant-mcp,
# sextant-dash) from the GitHub release tarballs; it does not compile from
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
  version "0.4.1"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.1/sextant_v0.4.1_darwin_arm64.tar.gz"
      sha256 "2cd2381dd189b81b72c4a05761eeb4c95a77650539167ba9b062d8f66c3e9308" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.1/sextant_v0.4.1_darwin_amd64.tar.gz"
      sha256 "65dd07d4a713f924ed2152249ac8283cf96c8ff78465ad550bcae8090c09a808" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.1/sextant_v0.4.1_linux_arm64.tar.gz"
      sha256 "04593654a2ae87a80b5ff83df685e45b0d610d5c5fba538617e97e38f9600fd3" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.1/sextant_v0.4.1_linux_amd64.tar.gz"
      sha256 "918537bac463317faad372045c2ce0e1b3990ad8c2efcd07129d6524e6c9cd42" # linux_amd64
    end
  end

  def install
    # Each tarball unpacks to a single top-level dir (Homebrew cds into it) with
    # bin/{sextant,sextant-mcp,sextant-dash}. Those three are the user-facing
    # binaries; install them all.
    bin.install "bin/sextant"
    bin.install "bin/sextant-mcp"
    bin.install "bin/sextant-dash"
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
