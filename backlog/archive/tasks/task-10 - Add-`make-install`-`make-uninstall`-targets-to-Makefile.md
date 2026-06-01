---
id: TASK-10
title: Add `make install` / `make uninstall` targets to Makefile
status: Done
assignee: []
created_date: '2026-05-24 23:18'
labels:
  - feature
  - build
  - ergonomics
  - 'slug:feat-make-install-target'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 10000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

Operators install sextant by running `make build` then manually `cp bin/* ~/.local/bin/`. This is repeated every time the binaries are updated. There's no `make install` target. Easy to forget after a `git pull`, leading to stale-binary surprises (see [[feat-doctor-stale-binary-detection]]).

## Proposed fix

Add to `Makefile`:

```makefile
PREFIX ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

install: build
	@mkdir -p $(INSTALL_DIR)
	@for cmd in $(CMDS); do \
	  install -m 0755 $(BIN_DIR)/$$cmd $(INSTALL_DIR)/$$cmd; \
	  echo ">> installed $(INSTALL_DIR)/$$cmd"; \
	done

uninstall:
	@for cmd in $(CMDS); do \
	  rm -f $(INSTALL_DIR)/$$cmd; \
	  echo ">> removed $(INSTALL_DIR)/$$cmd"; \
	done
```

Document in the README install section. `PREFIX` is overridable for system-wide installs (`sudo make install PREFIX=/usr/local`).

## Acceptance

1. `make install` puts every `$(CMDS)` binary into `$PREFIX/bin`
2. `which sextant && which sextantd && which sextant-shipper && which sextant-tui-agents` all resolve
3. `make uninstall` removes them; `which sextant` returns `not found`

## Postscript — Gatekeeper avoidance

The real reason `install -m 0755` is the right tool (vs. `cp`) is that `cp` on
macOS stamps `com.apple.provenance` onto the destination, and Gatekeeper
SIGKILLs cp'd Go binaries on invocation (exit 137, silent). `/usr/bin/install`
writes a clean file with no provenance xattr, so the installed binary runs
without Gatekeeper interference. Documented in
[[docs-install-via-make-install-not-cp]].

## Related

- [[feat-doctor-stale-binary-detection]] (related — stale-binary detection mitigates the "forgot to reinstall" footgun)
- [[docs-install-via-make-install-not-cp]] (related — documents the Gatekeeper-avoidance rationale this target relies on)
- README install section needs update
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-make-install-target.md
Discovered in: operator installation flow
Original created_at: 2026-05-24T23:18-07:00
Resolved at: 2026-05-25T01:16-07:00
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added `install` (depends on `build`) and `uninstall` targets to Makefile with overridable `PREFIX ?= $(HOME)/.local`; both iterate `$(CMDS)`.
<!-- SECTION:FINAL_SUMMARY:END -->
