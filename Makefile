# The static-checks gate and supporting targets. The gate's design (what each
# linter enforces and why) is docs/agents/go-static-checks.md; the judgment
# layer it pairs with is the go-house-style skill (.claude/skills/).
#
# golangci-lint is pinned here (gofumpt and govulncheck are pinned as go.mod
# tool directives) so local, CI, and agent runs never disagree.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

.PHONY: check fmt fmt-check tidy-check build lint fix-check vuln test e2e hooks book

# The full gate, fail-fast in cheapest-first order; the race suite runs last so
# it never burns minutes on a tree that fails formatting. CI runs these same
# targets (plus e2e) and is authoritative.
check: fmt-check tidy-check build lint fix-check vuln test

# Format the tree with gofumpt (stricter gofmt superset).
fmt:
	go tool gofumpt -w .

fmt-check:
	@files=$$(go tool gofumpt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofumpt: the following files need formatting (run 'make fmt'):"; \
		echo "$$files"; \
		exit 1; \
	fi

# Keep go.mod/go.sum honest (fails on drift).
tidy-check:
	go mod tidy -diff

build:
	go build ./...

# The curated linter set in .golangci.yml (includes go vet).
lint:
	$(GOLANGCI_LINT) run

# Modernizer gate: go fix exits 0 even when fixes apply, so fail on output.
fix-check:
	@diff=$$(go fix -diff ./... 2>&1); \
	if [ -n "$$diff" ]; then \
		echo "$$diff"; \
		echo "go fix: the tree has modernizer fixes (run 'go fix ./...')"; \
		exit 1; \
	fi

# Known-vulnerable dependencies the code actually reaches.
vuln:
	go tool govulncheck ./...

# Run the Go test suite under the race detector.
test:
	go test ./... -race

# The e2e acceptance suite (tests/e2e/m2-acceptance.md), behind the e2e tag.
e2e:
	go test -tags e2e -count=1 ./tests/e2e/

# Install the pre-commit hook that runs the gate.
hooks:
	git config core.hooksPath .githooks

# Build the mdbook reference into docs/book/book (gitignored output).
# Regenerates the generated pages from canon first (docgen), then renders.
# Install mdbook with: cargo install mdbook (or `brew install mdbook`).
book:
	go run ./cmd/docgen
	mdbook build docs/book
