BINARY   ?= phi
MAIN_SRC  = ./cmd

GOBIN    ?= $(shell go env GOBIN)
GOPATH   ?= $(shell go env GOPATH)
ifeq ($(GOBIN),)
GOBIN     = $(GOPATH)/bin
endif

GO       ?= go
GOFLAGS  ?= -ldflags="-s -w"
CGO      ?= 0

# Pin digest so CI / local make use the same markdownlint image.
MARKDOWN_LINT_IMAGE ?= avtodev/markdown-lint:v1@sha256:6aeedc2f49138ce7a1cd0adffc1b1c0321b841dc2102408967d9301c031949ee

.PHONY: all build install run clean test fmt fmt-check lint lint-markdown deadcode generate generate-check sync-models check help

all: build

build:
	CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) -o $(BINARY) $(MAIN_SRC)

install: build
	@mkdir -p $(GOBIN)
	mv $(BINARY) $(GOBIN)/$(BINARY)
	@echo "installed $(BINARY) -> $(GOBIN)/$(BINARY)"

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
	$(GO) clean

test:
	$(GO) test ./...
	$(GO) test -C ext/go ./...

# Regenerate ext/go/pxb/msg_gen.go from ext/go/pxb/schema.go.
generate:
	cd ext/go && $(GO) generate ./...

# Fail if the generated wire code is stale (used by CI).
generate-check:
	cd ext/go/pxb && $(GO) run ../internal/pxbgen -check

# Sync built-in preset specs (specs_gen.go) from models.dev.
sync-models:
	$(GO) run ./internal/project/model/cmd/modelgen

# Rust extension SDK (ext/rust): build + test.
test-rust:
	cd ext/rust && cargo test --all-targets

# Apply gofumpt / goimports / golines via .golangci.yml formatters.
fmt:
	golangci-lint fmt ./...
	cd ext/go && golangci-lint fmt ./...

# Fail if formatting would change files (used by CI).
fmt-check:
	golangci-lint fmt --diff ./...
	cd ext/go && golangci-lint fmt --diff ./...

lint:
	golangci-lint run ./...
	cd ext/go && golangci-lint run ./...

# Markdown structure lint (Docker). Needs a local Docker daemon.
lint-markdown:
	docker run --rm -v "$(CURDIR):/work" -w /work $(MARKDOWN_LINT_IMAGE) -c /work/.markdownlint.yaml $$(find . -name '*.md' -type f | sort)

deadcode:
	./scripts/deadcode-check.sh

check: fmt-check lint deadcode generate-check

# Rust extension SDK checks: format + lint (CI mirrors this).
check-rust:
	cd ext/rust && cargo fmt --check && cargo clippy --all-targets -- -D warnings

help:
	@echo "Usage:"
	@echo "  make          - build binary ($(BINARY))"
	@echo "  make install  - build & install to \$$GOBIN ($(GOBIN))"
	@echo "  make run      - build & run"
	@echo "  make clean    - remove binary & cache"
	@echo "  make test     - run all tests (root + nested ext module)"
	@echo "  make fmt      - format Go sources (gofumpt/goimports/golines)"
	@echo "  make fmt-check - check formatting without writing (CI)"
	@echo "  make lint     - run golangci-lint"
	@echo "  make lint-markdown - lint Markdown (Docker; needs daemon)"
	@echo "  make deadcode - unreachable func check (deadcode -test vs baseline)"
	@echo "  make generate - regenerate ext/go/pxb/msg_gen.go from schema.go"
	@echo "  make generate-check - fail if the generated wire code is stale (CI)"
	@echo "  make sync-models - sync built-in preset specs from models.dev"
	@echo "  make check    - fmt-check + lint + deadcode + generate-check (CI)"
	@echo "  make test-rust - test Rust extension SDK (ext/rust)"
	@echo "  make check-rust - format + lint Rust extension SDK (CI)"
