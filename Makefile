APP_NAME := pgdrivebackup
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(APP_NAME)
SLACK_NOTIFY_NAME := codex-slack-notify
SLACK_NOTIFY_PATH := $(BIN_DIR)/$(SLACK_NOTIFY_NAME)
SLACK_NOTIFY_WINDOWS_PATH := $(BIN_DIR)/$(SLACK_NOTIFY_NAME).exe
CACHE_DIR := .cache
GO_ENV := GOCACHE=$(abspath $(CACHE_DIR)/go-build) GOMODCACHE=$(abspath $(CACHE_DIR)/go-mod)

.PHONY: build build-pgdrivebackup build-slack-notify build-slack-notify-windows test run dry-run gitleaks clean

build: build-pgdrivebackup build-slack-notify build-slack-notify-windows

build-pgdrivebackup:
	mkdir -p $(BIN_DIR)
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	$(GO_ENV) go build -o $(BIN_PATH) ./cmd/$(APP_NAME)

build-slack-notify:
	mkdir -p $(BIN_DIR)
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	$(GO_ENV) go build -o $(SLACK_NOTIFY_PATH) ./cmd/$(SLACK_NOTIFY_NAME)

build-slack-notify-windows:
	mkdir -p $(BIN_DIR)
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	GOOS=windows GOARCH=amd64 $(GO_ENV) go build -o $(SLACK_NOTIFY_WINDOWS_PATH) ./cmd/$(SLACK_NOTIFY_NAME)

test:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	$(GO_ENV) go test ./...

run:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	$(GO_ENV) go run ./cmd/$(APP_NAME) --config configs/example.yaml

dry-run:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	$(GO_ENV) go run ./cmd/$(APP_NAME) --config configs/example.yaml --dry-run

gitleaks:
	gitleaks git --config .gitleaks.toml --redact .

clean:
	rm -rf $(BIN_DIR) $(CACHE_DIR)
