GO      ?= go
BINDIR  ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
ENV_FILES ?= .env .env.dev
ARGS ?=
EXPORT ?= data/report_2026-08-28_144610.csv

BINARIES := wallet-migrate wallet-verify

.PHONY: build lint test test-e2e tidy vuln check-categories $(addprefix run-,$(BINARIES))

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

# Live end-to-end tests against a DISPOSABLE Wallet. Gated by the `e2e` build
# tag and APP_WALLET_E2E=1; sources .env then .env.e2e (gitignored) if present.
# Never part of `make test` / CI-by-default. Cleanup: the suite deletes the
# records it creates; accounts/categories persist (the API has no delete) — wipe
# the test Wallet's data periodically.
test-e2e:
	@set -a; for f in .env .env.e2e; do [ -f $$f ] && . ./$$f; done; set +a; \
	APP_WALLET_E2E=$${APP_WALLET_E2E:-1} $(GO) test -tags e2e -run E2E -count=1 ./...

tidy:
	$(GO) mod tidy

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Pre-migration gate: resolve every export category against the LIVE Wallet
# catalogue + categories-alias.csv (via `wallet-migrate --dry-run`) and fail if
# any category is unresolved or an alias row is broken. Re-run after each
# categories-alias.csv edit before the real `wallet-migrate --create-missing`.
# Override the export with `make check-categories EXPORT=path/to/export.csv`.
check-categories:
	@set -a; for f in $(ENV_FILES); do [ -f $$f ] && . ./$$f; done; set +a; \
	out=$$($(GO) run ./cmd/wallet-migrate --export $(EXPORT) --create-missing --dry-run 2>&1); \
	echo "$$out"; \
	if printf '%s\n' "$$out" | grep -qe '^  ! '; then \
		echo "check-categories: FAIL — alias/category problems listed above"; exit 1; \
	fi; \
	none=$$(sed -n 's/^categories none: *//p' "$${APP_OUTPUT_DIR:-out}/_load_summary.txt" 2>/dev/null); \
	if [ "$$none" != "0" ]; then \
		echo "check-categories: FAIL — $${none:-?} categories unresolved (want 0)"; exit 1; \
	fi; \
	echo "check-categories: OK — every export category resolves (none: 0)"

define run-tool
	@set -a; for f in $(ENV_FILES); do [ -f $$f ] && . ./$$f; done; set +a; \
	$(GO) run ./cmd/$(1) $(ARGS)
endef

run-wallet-migrate:
	$(call run-tool,wallet-migrate)

run-wallet-verify:
	$(call run-tool,wallet-verify)
