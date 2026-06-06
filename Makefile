.PHONY: test vet lint fmt

# Run the Go test suite.
test:
	go test ./... -race

# go vet across the module.
vet:
	go vet ./...

# Lint: vet + a gofumpt formatting check (gofumpt is stricter than gofmt).
# Install gofumpt with: go install mvdan.cc/gofumpt@latest
lint: vet
	@files=$$(gofumpt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofumpt: the following files need formatting (run 'make fmt'):"; \
		echo "$$files"; \
		exit 1; \
	fi

# Format the tree with gofumpt.
fmt:
	gofumpt -w .
