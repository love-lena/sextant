---
title: Add `make install` / `make uninstall` targets to Makefile
status: resolved
priority: P3
created_at: 2026-05-24T23:18-07:00
resolved_at: 2026-05-25T01:16-07:00
resolution: Added `install` (depends on `build`) and `uninstall` targets to Makefile with overridable `PREFIX ?= $(HOME)/.local`; both iterate `$(CMDS)`.
labels: [feature, build, ergonomics]
discovered_in: operator installation flow
---

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

## Related

- [[feat-doctor-stale-binary-detection]] (related — stale-binary detection mitigates the "forgot to reinstall" footgun)
- README install section needs update
