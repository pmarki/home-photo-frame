.PHONY: setup install-go deps icons frontend-install dev backend-dev frontend-dev build start test clean

GO        ?= $(shell which go 2>/dev/null || echo $(HOME)/go/bin/go)
GOVERSION := 1.22.5
BINARY    := photo-frame
BUILD_NUM := $(shell date +%Y%m%d-%H%M)

# ── First-time setup ─────────────────────────────────────────────────
setup: install-go deps icons frontend-install
	@echo ""
	@echo "Setup complete. Drop images into photos/ then run: make build && ./$(BINARY)"

install-go:
	@if ! command -v go >/dev/null 2>&1 && ! [ -f $(HOME)/go/bin/go ]; then \
		echo "Installing Go $(GOVERSION)..."; \
		mkdir -p $(HOME)/sdk; \
		curl -fsSL https://go.dev/dl/go$(GOVERSION).linux-amd64.tar.gz | tar xz -C $(HOME)/sdk; \
		mv $(HOME)/sdk/go $(HOME)/sdk/go$(GOVERSION); \
		mkdir -p $(HOME)/go/bin; \
		ln -sf $(HOME)/sdk/go$(GOVERSION)/bin/go $(HOME)/go/bin/go; \
		echo "export PATH=\$$PATH:$(HOME)/go/bin" >> $(HOME)/.bashrc; \
		echo "Go installed at $(HOME)/sdk/go$(GOVERSION)"; \
	else \
		echo "Go: $$($(GO) version)"; \
	fi

deps:
	$(GO) mod tidy

icons:
	$(GO) run ./cmd/genicons

frontend-install:
	cd frontend && npm install

# ── Development ───────────────────────────────────────────────────────
# Terminal 1: make backend-dev   (Go server, reads ./frontend/dist from disk)
# Terminal 2: make frontend-dev  (Vite on :5173, proxies /api → :8080)
backend-dev:
	$(GO) run -tags dev ./cmd/server/ -photos ./photos -cache ./cache

frontend-dev:
	cd frontend && npm run dev

dev:
	@echo "Run in two separate terminals:"
	@echo "  make backend-dev"
	@echo "  make frontend-dev"
	@echo "Then open http://localhost:5173"

# ── Production build ──────────────────────────────────────────────────
# Builds the frontend, then compiles a single self-contained binary with
# the frontend embedded via go:embed (no -tags dev = production mode).
build: icons
	@echo "Building frontend..."
	cd frontend && npm run build
	@echo "Staging embedded assets..."
	rm -rf cmd/server/frontend/dist
	cp -r frontend/dist cmd/server/frontend/dist
	@echo "Compiling binary with embedded frontend..."
	CGO_ENABLED=0 $(GO) build -ldflags="-s -w -X main.buildNumber=$(BUILD_NUM)" -o $(BINARY) ./cmd/server/
	@echo ""
	@echo "Binary ready: ./$(BINARY)"
	@echo "Run:  ./$(BINARY) -photos ./photos"

start: build
	./$(BINARY) -photos ./photos -cache ./cache

# ── Tests ─────────────────────────────────────────────────────────────
test:
	$(GO) test -race ./cmd/server/

# ── Clean ─────────────────────────────────────────────────────────────
clean:
	rm -rf frontend/dist frontend/node_modules cache/*.thumb.jpg $(BINARY) cmd/server/frontend/
