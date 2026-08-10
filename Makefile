BINARY  := scm-bench
PKG     := github.com/scm-bench/scm-bench
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/cli.Version=$(VERSION) \
	-X $(PKG)/internal/cli.Commit=$(COMMIT) \
	-X $(PKG)/internal/cli.Date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

.PHONY: install
install: ## Install the binary into GOPATH/bin
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/$(BINARY)

.PHONY: test
test: ## Run the test suite
	go test ./... -race -count=1

.PHONY: cover
cover: ## Run tests and open a coverage report
	go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage written to coverage.html"

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format Go and Rego sources
	gofmt -s -w .
	@command -v opa >/dev/null 2>&1 && opa fmt -w internal/checks/policies || \
		echo "opa not installed; skipping Rego formatting"

.PHONY: fmt-check
fmt-check: ## Fail if sources are not formatted
	@unformatted=$$(gofmt -s -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "these files need gofmt -s -w:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: lint
lint: fmt-check vet ## Run formatting and vet checks

.PHONY: check
check: lint test ## Run everything CI runs

.PHONY: snapshot
snapshot: ## Build a local release with goreleaser, without publishing
	goreleaser release --snapshot --clean

.PHONY: docker
docker: ## Build the container image locally
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(BINARY):$(VERSION) .

.PHONY: checks
checks: build ## List the controls in the policy bundle
	./bin/$(BINARY) list-checks

.PHONY: clean
clean: ## Remove build output
	rm -rf bin dist coverage.out coverage.html
