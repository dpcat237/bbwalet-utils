GO      ?= go
BINDIR  ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
ENV_FILES ?= .env .env.dev
ARGS ?=

BINARIES := wallet-migrate

.PHONY: build lint test tidy vuln $(addprefix run-,$(BINARIES))

build:
	@mkdir -p $(BINDIR)
	@for b in $(BINARIES); do \
		echo "building $$b"; \
		$(GO) build -ldflags '$(LDFLAGS)' -o $(BINDIR)/$$b ./cmd/$$b || exit 1; \
	done

lint:
	golangci-lint run ./...

test:
	$(GO) test -race ./...

tidy:
	$(GO) mod tidy

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

run-wallet-migrate:
	@set -a; for f in $(ENV_FILES); do [ -f $$f ] && . ./$$f; done; set +a; \
	$(GO) run ./cmd/$(patsubst run-%,%,$@) $(ARGS)
