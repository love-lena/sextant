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
  version "0.4.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.0/sextant_v0.4.0_darwin_arm64.tar.gz"
      sha256 "8672dff46f8359b534662f136a924b74edb7532f917366acecef06a19f69e5a5" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.0/sextant_v0.4.0_darwin_amd64.tar.gz"
      sha256 "7023770eef1b2f0e5a476b9fa009946b2357395bfa6c358f34a5a92f8f54f86a" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.0/sextant_v0.4.0_linux_arm64.tar.gz"
      sha256 "132acb70235e6e78df5d73972e374a3edc9868af5246e57ef68d482e0c09d831" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.4.0/sextant_v0.4.0_linux_amd64.tar.gz"
      sha256 "d2f759ee48f60aa14ed6cb1a8d48b65e4c5626c4ff5da058940dd289912a8d5b" # linux_amd64
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
