#!/usr/bin/env bash
# scripts/bootstrap.sh — green-field setup for sextant.
#
# Idempotent: safe to re-run after git pull, or to recover a
# half-broken install.
#
# Usage:
#   ./scripts/bootstrap.sh           # interactive
#   ./scripts/bootstrap.sh --yes     # non-interactive (CI / repeat runs)
#   ./scripts/bootstrap.sh --skip-init   # skip the `sextant init` step
#   ./scripts/bootstrap.sh --help

set -euo pipefail

YES=0
SKIP_INIT=0

usage() {
  cat <<EOF
usage: scripts/bootstrap.sh [--yes] [--skip-init] [--help]

Brings a fresh host to a working sextant install:
  1. Audit host deps (Go, nats-server, clickhouse, docker, node)
  2. Print the install plan and prompt Y/n
  3. brew (macOS) or apt (Linux) install missing deps
  4. make install
  5. sextant doctor --preflight
  6. sextant init

--yes / -y     non-interactive; assume yes to the install prompt
--skip-init    install deps and binaries but don't write config
--help / -h    print this message

macOS via brew is the tested path. Linux via apt is partial —
nats-server and clickhouse aren't in default apt repos, so the
script prints upstream URLs and exits if they're missing.
EOF
}

for arg in "$@"; do
  case "$arg" in
    -y|--yes) YES=1 ;;
    --skip-init) SKIP_INIT=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; usage; exit 2 ;;
  esac
done

confirm() {
  if [[ "$YES" == "1" ]]; then
    return 0
  fi
  if [[ ! -t 0 ]]; then
    echo "Non-interactive stdin; assuming yes. Re-run with --yes to silence this warning."
    return 0
  fi
  local prompt="$1"
  read -r -p "$prompt [Y/n] " ans
  case "${ans:-y}" in
    [Yy]*) return 0 ;;
    *) echo "aborted."; exit 1 ;;
  esac
}

# version_ge $a $b returns 0 (true) if $a >= $b for dotted-number versions.
# Strips a leading "v" or "go" prefix if present.
version_ge() {
  local a="${1#v}"; a="${a#go}"
  local b="${2#v}"; b="${b#go}"
  local IFS=.
  # shellcheck disable=SC2206 # word-splitting on '.' is intentional
  local -a aparts=($a) bparts=($b)
  local i
  for ((i=0; i < ${#aparts[@]} || i < ${#bparts[@]}; i++)); do
    local av=${aparts[i]:-0} bv=${bparts[i]:-0}
    if (( 10#$av < 10#$bv )); then return 1; fi
    if (( 10#$av > 10#$bv )); then return 0; fi
  done
  return 0
}

# ---------------------------------------------------------------------------
# 1. Hard prerequisites
# ---------------------------------------------------------------------------
for tool in make git uname; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required tool '$tool' not found." >&2
    echo "install Xcode Command Line Tools (macOS: xcode-select --install) or build-essential (Linux)." >&2
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# 2. Detect OS + package manager
# ---------------------------------------------------------------------------
OS="$(uname -s)"
PKGMGR=""
case "$OS" in
  Darwin)
    if ! command -v brew >/dev/null 2>&1; then
      echo "Homebrew not found. Install it from https://brew.sh and re-run." >&2
      exit 1
    fi
    PKGMGR=brew
    ;;
  Linux)
    if command -v apt-get >/dev/null 2>&1; then
      PKGMGR=apt
    elif command -v brew >/dev/null 2>&1; then
      PKGMGR=brew
    else
      echo "no supported package manager (apt or brew) found." >&2
      exit 1
    fi
    ;;
  *)
    echo "unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# ---------------------------------------------------------------------------
# 3. Audit phase — no side effects
# ---------------------------------------------------------------------------
need_go=0
go_detail=""
if ! command -v go >/dev/null 2>&1; then
  need_go=1
  go_detail="missing"
else
  go_ver=$(go version | awk '{print $3}' | sed 's/go//')
  if ! version_ge "$go_ver" "1.26"; then
    need_go=1
    go_detail="found $go_ver, need >= 1.26"
  fi
fi

need_deps=()
for dep in nats-server clickhouse docker node; do
  if ! command -v "$dep" >/dev/null 2>&1; then
    need_deps+=("$dep")
  fi
done

docker_daemon_note=""
if command -v docker >/dev/null 2>&1; then
  if ! docker info >/dev/null 2>&1; then
    docker_daemon_note="docker binary present but daemon not reachable; start OrbStack manually"
  fi
fi

# ---------------------------------------------------------------------------
# 4. Linux escape hatch — bail if asked to install deps apt doesn't ship
# ---------------------------------------------------------------------------
if [[ "$PKGMGR" == "apt" ]]; then
  blockers=()
  for d in "${need_deps[@]+"${need_deps[@]}"}"; do
    if [[ "$d" == "nats-server" || "$d" == "clickhouse" ]]; then
      blockers+=("$d")
    fi
  done
  if [[ ${#blockers[@]} -gt 0 ]]; then
    echo "Linux apt path can't install: ${blockers[*]}"
    echo "Install them manually:"
    for b in "${blockers[@]}"; do
      case "$b" in
        nats-server) echo "  - nats-server: https://github.com/nats-io/nats-server/releases" ;;
        clickhouse)  echo "  - clickhouse:  https://clickhouse.com/docs/en/install" ;;
      esac
    done
    echo "Re-run scripts/bootstrap.sh after they're on PATH."
    exit 1
  fi
fi

# ---------------------------------------------------------------------------
# 5. Plan + confirm
# ---------------------------------------------------------------------------
echo "=== sextant bootstrap plan ==="
echo "Package manager: $PKGMGR"
if [[ "$OS" == "Linux" ]]; then
  echo "Note: Linux path is unverified; macOS is the tested target."
fi
echo ""

planned=0
if [[ "$need_go" == "1" ]]; then
  echo "  - install Go (>= 1.26) [$go_detail]"
  planned=1
fi
for d in "${need_deps[@]+"${need_deps[@]}"}"; do
  case "$d" in
    docker)
      if [[ "$PKGMGR" == "brew" ]]; then
        echo "  - install OrbStack (docker; install Docker Desktop manually if you prefer)"
      else
        echo "  - install docker.io"
      fi
      ;;
    *)      echo "  - install $d" ;;
  esac
  planned=1
done
if [[ -n "$docker_daemon_note" ]]; then
  echo "  note: $docker_daemon_note"
fi
echo "  - make install"
echo "  - sextant doctor --preflight"
if [[ "$SKIP_INIT" == "0" ]]; then
  echo "  - sextant init"
fi
echo ""

if [[ "$planned" == "1" ]]; then
  confirm "Proceed?"
else
  echo "All dependencies present."
fi

# ---------------------------------------------------------------------------
# 6. Install Go first (required for make install)
# ---------------------------------------------------------------------------
if [[ "$need_go" == "1" ]]; then
  case "$PKGMGR" in
    brew) brew install go ;;
    apt)
      echo "apt path: see https://go.dev/dl for >= 1.26; apt's golang is usually too old."
      exit 1
      ;;
  esac
fi

# ---------------------------------------------------------------------------
# 7. Install runtime deps
# ---------------------------------------------------------------------------
for d in "${need_deps[@]+"${need_deps[@]}"}"; do
  case "$PKGMGR" in
    brew)
      case "$d" in
        docker) brew install --cask orbstack ;;
        *)      brew install "$d" ;;
      esac
      ;;
    apt)
      case "$d" in
        docker) sudo apt-get install -y docker.io ;;
        node)   sudo apt-get install -y nodejs npm ;;
      esac
      ;;
  esac
done

# ---------------------------------------------------------------------------
# 8. make install
# ---------------------------------------------------------------------------
make install

# ---------------------------------------------------------------------------
# 8b. Backlog.md CLI — pinned tooling for managing tickets in backlog/
#     (see .claude/skills/backlog). Skipped automatically if already present.
# ---------------------------------------------------------------------------
make backlog-install

# ---------------------------------------------------------------------------
# 9. Preflight: now sextant exists, run the Go-side check
# ---------------------------------------------------------------------------
SEXTANT="${HOME}/.local/bin/sextant"
if [[ ! -x "$SEXTANT" ]]; then
  echo "make install completed but $SEXTANT is not executable." >&2
  exit 1
fi
if ! "$SEXTANT" doctor --preflight; then
  echo ""
  echo "preflight failed; resolve the issues above and re-run."
  exit 1
fi

# ---------------------------------------------------------------------------
# 10. sextant init
# ---------------------------------------------------------------------------
if [[ "$SKIP_INIT" == "0" ]]; then
  "$SEXTANT" init
fi

echo ""
echo "Bootstrap complete."
echo "Next: sextant start && sextant doctor"

exit 0
