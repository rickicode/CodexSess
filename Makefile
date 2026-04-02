.PHONY: build build-frontend build-backend build-linux-assets build-windows run dev wails wails-dev kill clean test deps

BINARY_NAME ?= codexsess
BINARY_PATH ?= ./$(BINARY_NAME)
WEB_DIR ?= web
EMBED_ASSETS_DIR ?= internal/webui/assets
DEV_FRONTEND_PORT ?= 3051
DEV_BACKEND_PORT ?= 3052
RUN_PORT ?= 3061
APP_VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
TEST_TIMEOUT ?= 120s
TEST_PKGS ?= ./...
TEST_P ?= 2
TEST_ARGS ?=

build: build-frontend build-linux-assets build-backend

build-frontend:
	@echo "Building frontend from $(WEB_DIR)..."
	cd $(WEB_DIR) && npm install && npm run build:web
	@echo "Frontend build output is configured directly to $(EMBED_ASSETS_DIR) via Vite outDir."

build-linux-assets:
	@echo "Generating Linux app icon + desktop launcher..."
	@mkdir -p build/linux
	@go run ./scripts/gen_default_icon.go build/linux/codexsess.png
	@printf '%s\n' \
		'[Desktop Entry]' \
		'Type=Application' \
		'Name=CodexSess' \
		'Comment=Codex Account Management' \
		'Exec='\"$$(pwd)/$(BINARY_NAME)\" \
		'Icon='\"$$(pwd)/build/linux/codexsess.png\" \
		'Terminal=true' \
		'Categories=Development;Utility;' \
		> build/linux/codexsess.desktop
	@echo "Linux assets: build/linux/codexsess.png and build/linux/codexsess.desktop"

build-backend:
	@echo "Building backend binary $(BINARY_PATH)..."
	go build -ldflags "-X main.appVersion=$(APP_VERSION)" -o $(BINARY_PATH) .

build-windows: build-frontend
	@echo "Building Windows binary with default icon..."
	@mkdir -p build/windows
	@go run ./scripts/gen_default_icon.go build/windows/app.ico
	@go run ./scripts/gen_default_icon.go build/windows/app.png
	@go run github.com/akavel/rsrc@latest -ico build/windows/app.ico -o codexsess_windows_amd64.syso
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.appVersion=$(APP_VERSION)" -o codexsess.exe .
	@echo "Windows assets: codexsess.exe + build/windows/app.png"

run: build
	@echo "Starting $(BINARY_NAME) on port $(RUN_PORT)..."
	PORT=$(RUN_PORT) CODEXSESS_NO_OPEN_BROWSER=1 $(BINARY_PATH)

dev:
	@echo "Starting dev mode (frontend: $(DEV_FRONTEND_PORT), backend: $(DEV_BACKEND_PORT))..."
	@command -v air >/dev/null 2>&1 || { \
		echo "Installing air..."; \
		go install github.com/air-verse/air@latest; \
	}
	@set -e; \
	trap 'kill 0' INT TERM EXIT; \
	(cd $(WEB_DIR) && npm install && npm run dev -- --host 127.0.0.1 --port $(DEV_FRONTEND_PORT)) & \
	PORT=$(DEV_BACKEND_PORT) CODEXSESS_NO_OPEN_BROWSER=1 "$$(go env GOPATH)/bin/air" -c .air.toml

wails:
	@echo "Wails mode removed. Building web-only binary..."
	$(MAKE) build

wails-dev:
	@echo "Wails mode removed. Running web dev mode..."
	$(MAKE) dev

kill:
	@echo "Stopping codexsess dev/run processes on ports $(DEV_FRONTEND_PORT), $(DEV_BACKEND_PORT), $(RUN_PORT)..."
	-pkill -f "air -c .air.toml"
	-pkill -f "$(BINARY_NAME)"
	-pkill -f "vite.*$(DEV_FRONTEND_PORT)"
	-fuser -k $(DEV_FRONTEND_PORT)/tcp $(DEV_BACKEND_PORT)/tcp $(RUN_PORT)/tcp 2>/dev/null || true

clean:
	@echo "Cleaning build artifacts..."
	go clean
	rm -f $(BINARY_PATH)
	@echo "Directory cleanup skipped (no rm dir policy)."

test:
	go test -timeout $(TEST_TIMEOUT) -p $(TEST_P) $(TEST_ARGS) $(TEST_PKGS)

deps:
	go mod download
	cd $(WEB_DIR) && npm install
