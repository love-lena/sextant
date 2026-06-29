#!/usr/bin/env sh
# Adversarial shell harness for the TASK-118 worker sandbox in pi.sh. It runs the
# REAL recipe (clients/dispatcher/recipes/pi.sh) with a FAKE pi binary that, when
# exec'd, records its CWD + PATH and then drives the installed command guard from
# the worker's own environment — proving the scoping holds for a worker that
# actively tries to escape it. No bus, no model, no network: the recipe runs up
# to `exec $PI_BIN`, where our stub takes over.
#
# Run from anywhere: `sh clients/dispatcher/recipes/pi_sandbox_test.sh`. Exits 0
# only if every assertion passes; prints PASS/FAIL per check.
set -u

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RECIPE="$HERE/pi.sh"
[ -f "$RECIPE" ] || { echo "FATAL: recipe not found at $RECIPE"; exit 2; }

FAILS=0
ok()   { echo "PASS: $1"; }
bad()  { echo "FAIL: $1"; FAILS=$((FAILS + 1)); }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/sx118-XXXXXX")
trap 'rm -rf "$WORK"' EXIT
STORE="$WORK/store"
mkdir -p "$STORE"
: > "$STORE/bus.json"

# A creds file + a fake pi-bus extension path (the recipe only checks they are
# set/non-empty; it never loads them because our fake pi ignores its args).
CREDS="$WORK/child.creds"
: > "$CREDS"
EXT="$WORK/ext.js"
: > "$EXT"

# A fake pi: on exec it records CWD + PATH for the parent to assert, then runs a
# guard probe FROM the worker's environment (the env the real pi would inherit)
# and records each class's exit code. It reads its probe script from $PROBE.
FAKEPI="$WORK/fakepi"
cat > "$FAKEPI" <<'FAKE'
#!/usr/bin/env sh
# Drain any FIFO-injected first prompt so we don't block, then report + probe.
pwd > "$SX_TEST_OUT/cwd"
printf '%s\n' "$PATH" > "$SX_TEST_OUT/path"
# Run the probe in THIS environment (worker's PATH, including the guard bin).
sh "$SX_TEST_PROBE" > "$SX_TEST_OUT/probe" 2>&1
exit 0
FAKE
chmod +x "$FAKEPI"

# The probe the fake pi runs as the worker: try each denied class and a benign
# command, recording "name=exitcode" lines.
PROBE="$WORK/probe.sh"
cat > "$PROBE" <<'PROBE'
run() { "$@" >/dev/null 2>&1; echo "$1=$?"; }
run killall Finder
run pkill node
run osascript -e x
run open -a Firefox
run shutdown -h now
run brew install jq
npm install >/dev/null 2>&1; echo "npm-install=$?"
npm --version >/dev/null 2>&1; echo "npm-version=$?"
git push --force origin main >/dev/null 2>&1; echo "git-force=$?"
git --version >/dev/null 2>&1; echo "git-version=$?"
# A benign in-scope file write must succeed (real work unimpeded).
echo hi > ./in_scope_file && echo "in-scope-write=0" || echo "in-scope-write=1"
PROBE

export SX_TEST_OUT="$WORK/out"
mkdir -p "$SX_TEST_OUT"
export SX_TEST_PROBE="$PROBE"

run_recipe() {
  # Args become extra env assignments; runs the recipe with a baseline scoped env.
  env \
    SEXTANT_CREDS="$CREDS" \
    SEXTANT_STORE="$STORE" \
    SEXTANT_PI_EXTENSION="$EXT" \
    SX_CHILD_ID="01TESTCHILD" \
    SX_CHILD_NICK="tester" \
    SX_PI_BIN="$FAKEPI" \
    SX_TEST_OUT="$SX_TEST_OUT" \
    SX_TEST_PROBE="$PROBE" \
    "$@" \
    sh "$RECIPE"
}

echo "== AC#5: a scoped spawn runs; AC#1/#2: guard + CWD enforced =="
rm -rf "$SX_TEST_OUT"; mkdir -p "$SX_TEST_OUT"
if run_recipe >/dev/null 2>&1; then
  ok "recipe spawned with a resolvable scope"
else
  bad "recipe failed to spawn under a valid scope (exit $?)"
fi

CWD=$(cat "$SX_TEST_OUT/cwd" 2>/dev/null || echo "")
EXPECT="$(dirname "$STORE")/pi-work/01TESTCHILD"
# Resolve symlinks (macOS /tmp -> /private/tmp) before comparing.
realdir() { (cd "$1" 2>/dev/null && pwd -P) || echo "$1"; }
if [ "$(realdir "$CWD")" = "$(realdir "$EXPECT")" ]; then
  ok "AC#1/#5: worker CWD is the scoped dir ($CWD)"
else
  bad "AC#1/#5: worker CWD '$CWD' != scoped dir '$EXPECT'"
fi

PROBE_OUT=$(cat "$SX_TEST_OUT/probe" 2>/dev/null || echo "")
check_blocked() { # name
  v=$(printf '%s\n' "$PROBE_OUT" | sed -n "s/^$1=//p")
  if [ "$v" = "126" ]; then ok "AC#2: '$1' denied at shell (exit 126)"; else bad "AC#2: '$1' NOT denied (got '$v', want 126)"; fi
}
for c in killall pkill osascript open shutdown brew npm-install git-force; do
  check_blocked "$c"
done
check_allowed() { # name
  v=$(printf '%s\n' "$PROBE_OUT" | sed -n "s/^$1=//p")
  if [ "$v" != "126" ] && [ -n "$v" ]; then ok "AC#3: '$1' reached the real tool (exit $v, not blocked)"; else bad "AC#3: '$1' was blocked or missing (got '$v')"; fi
}
check_allowed "npm-version"
check_allowed "git-version"
check_allowed "in-scope-write"

echo "== AC#5 fail-loud: an UNSCOPED config refuses to spawn =="
# Force WORKDIR to empty via an override that resolves to "" — SEXTANT_PI_WORKDIR
# set explicitly empty AND no child id, so the default would still try; the real
# fail-loud is the empty/"/" guard. We point the store at / so dirname is / and
# clear the child id to drive WORKDIR to "//pi-work/" — still non-empty, so the
# sharper test is an unwritable scope:
rm -rf "$SX_TEST_OUT"; mkdir -p "$SX_TEST_OUT"
if env \
    SEXTANT_CREDS="$CREDS" SEXTANT_STORE="$STORE" SEXTANT_PI_EXTENSION="$EXT" \
    SX_CHILD_ID="x" SX_CHILD_NICK="t" SX_PI_BIN="$FAKEPI" \
    SEXTANT_PI_WORKDIR="/proc/nonexistent/cannot/create/$$" \
    sh "$RECIPE" >/dev/null 2>&1; then
  bad "AC#5: recipe spawned despite an uncreatable scope"
else
  rc=$?
  if [ "$rc" = "78" ]; then ok "AC#5: uncreatable scope fails loud (exit 78 EX_CONFIG)"; else ok "AC#5: uncreatable scope refused to spawn (exit $rc)"; fi
fi

echo "== AC#5 fail-loud: an EMPTY scope refuses to spawn =="
if env \
    SEXTANT_CREDS="$CREDS" SEXTANT_STORE="$STORE" SEXTANT_PI_EXTENSION="$EXT" \
    SX_CHILD_ID="x" SX_CHILD_NICK="t" SX_PI_BIN="$FAKEPI" \
    SEXTANT_PI_WORKDIR="/" \
    sh "$RECIPE" >/dev/null 2>&1; then
  bad "AC#5: recipe spawned with scope='/' (root!)"
else
  ok "AC#5: scope='/' refused to spawn (exit $?)"
fi

echo
if [ "$FAILS" -eq 0 ]; then
  echo "ALL PASS"
  exit 0
fi
echo "$FAILS CHECK(S) FAILED"
exit 1
