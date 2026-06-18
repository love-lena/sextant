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
  version "0.5.1"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.1/sextant_v0.5.1_darwin_arm64.tar.gz"
      sha256 "013ef7783b20fe8ccef6e5c08e87920875a5b2bfc63916b4a07781c987f54a67" # darwin_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.1/sextant_v0.5.1_darwin_amd64.tar.gz"
      sha256 "9247891227283622d5d77a703ce00a95f3575b8667f1f081f253ecc99d5a2da3" # darwin_amd64
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.1/sextant_v0.5.1_linux_arm64.tar.gz"
      sha256 "c2677400ab671fc99b888e03c7e9ce3620fefb9f20c47210ddc06e71812c4ee6" # linux_arm64
    end
    if Hardware::CPU.intel?
      url "https://github.com/love-lena/sextant/releases/download/v0.5.1/sextant_v0.5.1_linux_amd64.tar.gz"
      sha256 "491bd2be78c91d0304c7498444e35a81b38fc455f3ee551aa34acfe4281828ab" # linux_amd64
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
