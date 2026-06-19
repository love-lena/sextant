.PHONY: test vet lint fmt book ui build

# Generate the dash web UI bundles (.jsx -> .js via esbuild). The .js are
# generated artifacts (gitignored, TASK-121) embedded by the Go build, so this
# must run before any go build/vet/test that compiles internal/dashapi.
ui:
	bash scripts/build-dash-ui.sh

# Build the sextant binary into ./bin (regenerates the dash bundles first, so
# the embedded UI is present). The cross-platform release build is scripts/release.sh.
build: ui
	go build -o bin/sextant ./clients/go/apps/sextant

# Run the Go test suite (regenerates the dash bundles first).
test: ui
	go test ./... -race

# go vet across the module (regenerates the dash bundles first).
vet: ui
	go vet ./...

# Lint: vet + the curated static-checks gate (.golangci.yml, ADR-0042) + a
# gofumpt formatting check (gofumpt is stricter than gofmt) + the import-discipline
# bright lines (importcheck, ADR-0041). The same gate runs in CI's Go job.
# Install golangci-lint v2 with: brew install golangci-lint (or the v2 release).
# Install gofumpt with: go install mvdan.cc/gofumpt@latest
lint: vet
	golangci-lint run ./...
	@files=$$(gofumpt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofumpt: the following files need formatting (run 'make fmt'):"; \
		echo "$$files"; \
		exit 1; \
	fi
	go test ./internal/importcheck/... ./bus/ ./clients/go/conventions/ ./clients/go/apps/internal/tui/...

# Format the tree with gofumpt.
fmt:
	gofumpt -w .

# Build the mdbook reference into docs/book/book (gitignored output).
# Regenerates the generated pages from canon first (docgen), then renders.
# Install mdbook with: cargo install mdbook (or `brew install mdbook`).
book:
	go run ./clients/go/apps/docgen
	mdbook build docs/book
