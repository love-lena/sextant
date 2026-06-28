.PHONY: test vet lint fmt book ui pi-ext gen-embeds build generate

# Generate the dash web UI bundles (.jsx -> .js via esbuild). The .js are
# generated artifacts (gitignored, TASK-121) embedded by the Go build, so this
# must run before any go build/vet/test that compiles internal/dashapi.
ui:
	bash scripts/build-dash-ui.sh

# Generate the pi-bus extension bundle (ADR-0052): a single self-contained ESM
# file esbuild-bundled from clients/pi-bus, gitignored, embedded by the Go build
# into the sextant binary. Like `ui`, it must run before any go build/vet/test
# that compiles clients/sextant-cli/internal/components.
pi-ext:
	bash scripts/build-pi-bus.sh

# Both generated embeds the Go build needs present to COMPILE.
gen-embeds: ui pi-ext

# Build the sextant binary into ./bin (regenerates the embeds first, so the
# embedded UI + pi-bus extension are present). The cross-platform release build
# is scripts/release.sh.
build: gen-embeds
	go build -o bin/sextant ./clients/sextant-cli

# Run the Go test suite (regenerates the embeds first).
test: gen-embeds
	go test ./... -race

# go vet across the module (regenerates the embeds first).
vet: gen-embeds
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
	go test ./internal/importcheck/... ./bus/ ./conventions/... ./clients/sextant-tui/internal/tui/...

# Regenerate code generated from the lexicon (ADR-0041): the per-language record
# types a convention library consumes. Today that is conv/goals' goal_gen.go,
# emitted from protocol/lexicons/goal.json by conventions/goal/go/internal/lexgen
# (a //go:generate directive in goals.go). Run after editing a lexicon whose Go
# types are generated; the output is committed and gofumpt-clean.
generate:
	go generate ./...
	gofumpt -w .

# Format the tree with gofumpt.
fmt:
	gofumpt -w .

# Build the mdbook reference into docs/book/book (gitignored output).
# Regenerates the generated pages from canon first (docgen), then renders.
# Install mdbook with: cargo install mdbook (or `brew install mdbook`).
book:
	go run ./sdk/docgen
	mdbook build docs/book
