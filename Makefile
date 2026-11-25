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

.PHONY: all build install run clean test fmt fmt-check lint lint-markdown deadcode check help

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

# Apply gofumpt / goimports / golines via .golangci.yml formatters.
fmt:
	golangci-lint fmt ./...

# Fail if formatting would change files (used by CI).
fmt-check:
	golangci-lint fmt --diff ./...

lint:
	golangci-lint run ./...

# Markdown structure lint (Docker). Needs a local Docker daemon.
lint-markdown:
	docker run --rm -v "$(CURDIR):/work" -w /work $(MARKDOWN_LINT_IMAGE) -c /work/.markdownlint.yaml $$(find . -name '*.md' -type f | sort)

deadcode:
	./scripts/deadcode-check.sh

check: fmt-check lint deadcode

help:
	@echo "Usage:"
	@echo "  make          - build binary ($(BINARY))"
	@echo "  make install  - build & install to \$$GOBIN ($(GOBIN))"
	@echo "  make run      - build & run"
	@echo "  make clean    - remove binary & cache"
	@echo "  make test     - run all tests"
	@echo "  make fmt      - format Go sources (gofumpt/goimports/golines)"
	@echo "  make fmt-check - check formatting without writing (CI)"
	@echo "  make lint     - run golangci-lint"
	@echo "  make lint-markdown - lint Markdown (Docker; needs daemon)"
	@echo "  make deadcode - unreachable func check (deadcode -test vs baseline)"
	@echo "  make check    - fmt-check + lint + deadcode (CI)"
