# flow — Makefile
# Targets:
#   make build         -> ./bin/flow
#   make install       -> install binary to ~/.local/bin (and restart scheduler if it's running)
#   make install-all   -> install + register systemd --user scheduler unit
#   make uninstall     -> remove binary + scheduler unit
#   make test          -> go test ./...
#   make clean         -> remove ./bin

BIN_DIR    ?= bin
BIN_NAME   ?= flow
INSTALL_DIR?= $(HOME)/.local/bin
INSTALL_BIN := $(INSTALL_DIR)/$(BIN_NAME)

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X 'github.com/yithcai/flow/cmd.version=$(VERSION)' \
	-X 'github.com/yithcai/flow/cmd.commit=$(COMMIT)' \
	-X 'github.com/yithcai/flow/cmd.date=$(DATE)'

.PHONY: build install install-all uninstall test clean tidy fmt vet

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BIN_NAME) .

install: build
	@mkdir -p $(INSTALL_DIR)
	@# Stop scheduler first if it's holding the old binary; we'll restart it after.
	@if command -v systemctl >/dev/null 2>&1 && \
	   systemctl --user is-active --quiet flow-scheduler.service 2>/dev/null; then \
	    echo "scheduler is running; stopping briefly to swap binary..."; \
	    systemctl --user stop flow-scheduler.service; \
	    SCHED_WAS_RUNNING=1; \
	else \
	    SCHED_WAS_RUNNING=0; \
	fi; \
	install -m 0755 $(BIN_DIR)/$(BIN_NAME) $(INSTALL_BIN); \
	echo "Installed: $(INSTALL_BIN)"; \
	if [ "$$SCHED_WAS_RUNNING" = "1" ]; then \
	    systemctl --user start flow-scheduler.service && \
	    echo "scheduler restarted with new binary."; \
	fi
	@echo ""
	@echo "Next steps:"
	@echo "  1. Enable directory jumping in your shell:"
	@echo "       eval \"\$$($(BIN_NAME) shell init bash)\"   # add to ~/.bashrc"
	@echo "  2. (Optional) install the scheduler for recurring tasks:"
	@echo "       make install-all                       # one-shot setup"
	@echo "       # or:  flow scheduler install"
	@echo "  3. Open the web UI:"
	@echo "       flow ui"

install-all: install
	@if ! command -v systemctl >/dev/null 2>&1; then \
	    echo "systemctl not found; skipping scheduler install."; \
	    echo "You can still run it manually:  nohup flow scheduler run > ~/.config/flow/scheduler.log 2>&1 &"; \
	    exit 0; \
	fi
	@$(INSTALL_BIN) scheduler install

uninstall:
	@if command -v systemctl >/dev/null 2>&1 && \
	   [ -f "$$HOME/.config/systemd/user/flow-scheduler.service" ]; then \
	    $(INSTALL_BIN) scheduler uninstall || true; \
	fi
	@rm -f $(INSTALL_BIN)
	@echo "Uninstalled flow."

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)
