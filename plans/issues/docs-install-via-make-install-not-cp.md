---
title: Document `make install` as the installation method; warn against plain `cp`
status: fixed
priority: P3
created_at: 2026-05-25T14:53-07:00
fixed_in: ee54920
labels: [docs, install, ergonomics, macos]
discovered_in: post-overnight rebuild + reinstall surprise
---

## Summary

`make install` (added by dev-3 at `e9b1dfb`) uses the `install` command which produces clean binaries that pass macOS Gatekeeper. Plain `cp bin/* ~/.local/bin/` sets `com.apple.provenance` xattr on the destination files, and Gatekeeper SIGKILLs cp'd Go binaries on invocation (exit 137, no stderr message — silent kill).

Hit this twice during the overnight session before realizing `cp` was the cause: rebuild → cp → `sextant --version` returns exit 137 → confusion. The fix is "use `make install`," but that's not documented anywhere a first-time operator would see.

## Repro

```bash
make build
cp bin/sextant ~/.local/bin/   # bad
xattr ~/.local/bin/sextant     # shows com.apple.provenance
sextant --version              # exit 137 (Gatekeeper kill); silent
```

vs.

```bash
make build
make install                   # uses `install -m 0755`, no provenance
sextant --version              # works
```

## Impact

- First-time installers (or operators rebuilding after a `git pull`) hit a silent failure that's near-impossible to diagnose without knowing about macOS provenance/Gatekeeper interaction.
- The error is exit code 137 with no output — looks like the binary itself is broken, not the install method.
- Hit during the overnight session; would hit again every time someone does the natural-feeling `cp`.

## Proposed fix

Two small docs touches; no code changes needed.

1. **README install section** — add an "Install" heading with the canonical workflow:

   ```bash
   git clone git@github.com:love-lena/sextant-initial.git
   cd sextant-initial
   make install            # NOT `cp bin/* ~/.local/bin/`; cp triggers macOS Gatekeeper SIGKILL.
                           # PREFIX overridable: `sudo make install PREFIX=/usr/local`
   sextant init
   sextantd &
   ```

2. **`sextant init` output** — at the end, print:

   ```
   note: sextant binaries should be installed via `make install` (not plain cp).
   macOS Gatekeeper SIGKILLs cp'd Go binaries due to the com.apple.provenance
   xattr. `make install` uses /usr/bin/install which avoids the issue.
   ```

   Conditional on detecting macOS (`runtime.GOOS == "darwin"`). On Linux this isn't a problem.

3. **Reference from `feat-make-install-target.md`** — that issue resolved the missing install target but didn't note the Gatekeeper-avoidance benefit, which is the real reason `install` is the right tool.

## Acceptance

`TestInitOutputMentionsMakeInstallOnMacOS`: run `sextant init --dry-run` on darwin; stdout contains the `make install` note. Skip on non-darwin.

README diff visible at PR review.

## Related

- `e9b1dfb feat(make): add install/uninstall targets with overridable PREFIX`
- `feat-make-install-target.md` (resolved)
- `[[feat-doctor-stale-binary-detection]]` (resolved at e9412a1) — doctor warns when installed binary lags HEAD, complementary to this docs change
- Overnight summary `plans/overnight-summary-2026-05-25.md` mentions the gotcha but only as a runtime note, not as a docs fix
