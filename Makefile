GO          ?= go
OUT_DIR     := ./bin

BOLD  := \033[1m
CYAN  := \033[36m
GREEN := \033[32m
RESET := \033[0m

.DEFAULT_GOAL := help

# ── Build ────────────────────────────────────────────────────────────────────

.PHONY: build

build: ## Build local lathe binary into ./bin/lathe
	@mkdir -p $(OUT_DIR)
	$(GO) build -trimpath -o $(OUT_DIR)/lathe ./cmd/lathe
	@printf '\n$(GREEN)  ✓ built $(CYAN)$(OUT_DIR)/lathe$(RESET)\n\n'

# ── Quality ──────────────────────────────────────────────────────────────────

.PHONY: check test vet fmt fmt-check lint

check: ## Full quality gate — fmt-check, vet, lint, test
	@printf '\n$(BOLD)[1/4] Checking format$(RESET)\n'
	@$(MAKE) --no-print-directory fmt-check
	@printf '\n$(BOLD)[2/4] Running vet$(RESET)\n'
	$(GO) vet ./...
	@printf '\n$(BOLD)[3/4] Running lint$(RESET)\n'
	@$(MAKE) --no-print-directory lint
	@printf '\n$(BOLD)[4/4] Running tests$(RESET)\n'
	$(GO) test ./...
	@printf '\n$(GREEN)  ✓ All checks passed$(RESET)\n\n'

lint: ## Run golangci-lint
	golangci-lint run ./...

test: ## Run tests
	$(GO) test ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format code in place
	$(GO) fmt ./...

fmt-check: ## Fail if any file needs gofmt
	@out=$$(gofmt -l cmd internal pkg); \
	if [ -n "$$out" ]; then \
	  printf '$(BOLD)gofmt violations:$(RESET)\n%s\n' "$$out"; \
	  exit 1; \
	fi

# ── Maintenance ──────────────────────────────────────────────────────────────

.PHONY: tidy clean

tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

clean: ## Remove build artifacts + generated code
	rm -rf $(OUT_DIR) internal/generated

# ── Help ─────────────────────────────────────────────────────────────────────

.PHONY: help

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "\n$(BOLD)lathe$(RESET) — spec-driven CLI generator\n"} \
		/^# ── / {n = $$0; gsub(/(^# ── | ─+$$)/, "", n); printf "\n$(BOLD)%s$(RESET)\n", n} \
		/^[a-zA-Z_-]+:.*## / {printf "  $(CYAN)make %-12s$(RESET) %s\n", $$1, $$2} \
		END {printf "\n"}' $(MAKEFILE_LIST)
