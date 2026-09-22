BINARY   ?= phi
MAIN_SRC  = ./cmd

# GO first: the env lookups below run the toolchain, and a caller that points
# GO at an absolute path is how a runner without go on PATH still works.
GO       ?= go

GOBIN    ?= $(shell $(GO) env GOBIN)
GOPATH   ?= $(shell $(GO) env GOPATH)
ifeq ($(GOBIN),)
GOBIN     = $(GOPATH)/bin
endif

GOFLAGS  ?= -ldflags="-s -w"
CGO      ?= 0

# Pin digest so CI / local make use the same markdownlint image.
MARKDOWN_LINT_IMAGE ?= avtodev/markdown-lint:v1@sha256:6aeedc2f49138ce7a1cd0adffc1b1c0321b841dc2102408967d9301c031949ee

.PHONY: all build install run clean test test-live fmt fmt-check lint lint-markdown deadcode evidence check help

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

# The OrcaRouter live check: a real catalog fetch and one chat completion
# through the shipped provider code. Needs ORCAROUTER_API_KEY; skips without it.
test-live:
	$(GO) test ./internal/orca/ -run Live -v -count=1

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

# Regenerate the OrcaRouter UI evidence (Playwright against a loopback
# catalog). Writes orca-evidence/, which is gitignored. Set GO to an absolute
# go path when the toolchain is not on PATH.
evidence:
	python3 orca-evidence-src/generate_evidence.py

check: fmt-check lint deadcode

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
	@echo "  make test-live - OrcaRouter live catalog + chat check (needs ORCAROUTER_API_KEY)"
	@echo "  make fmt      - format Go sources (gofumpt/goimports/golines)"
	@echo "  make fmt-check - check formatting without writing (CI)"
	@echo "  make lint     - run golangci-lint"
	@echo "  make lint-markdown - lint Markdown (Docker; needs daemon)"
	@echo "  make deadcode - unreachable func check (deadcode -test vs baseline)"
	@echo "  make evidence - regenerate OrcaRouter UI evidence (needs Playwright)"
	@echo "  make check    - fmt-check + lint + deadcode (CI)"
	@echo "  make test-rust - test Rust extension SDK (ext/rust)"
	@echo "  make check-rust - format + lint Rust extension SDK (CI)"
