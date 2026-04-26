APP_NAME := pgdrivebackup
APP_DIR := pgdrivebackup
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(APP_NAME)
SLACK_NOTIFY_NAME := codex-slack-notify
SLACK_NOTIFY_PATH := $(BIN_DIR)/$(SLACK_NOTIFY_NAME)
CACHE_DIR := .cache
GO_ENV := GOCACHE=$(abspath $(CACHE_DIR)/go-build) GOMODCACHE=$(abspath $(CACHE_DIR)/go-mod)

.PHONY: build build-pgdrivebackup build-slack-notify test run dry-run clean

build: build-pgdrivebackup build-slack-notify

build-pgdrivebackup:
	mkdir -p $(BIN_DIR)
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go build -o ../$(BIN_PATH) ./cmd/$(APP_NAME)

build-slack-notify:
	mkdir -p $(BIN_DIR)
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go build -o ../$(SLACK_NOTIFY_PATH) ./cmd/$(SLACK_NOTIFY_NAME)

test:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go test ./...

run:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go run ./cmd/$(APP_NAME) --config ../configs/example.yaml

dry-run:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go run ./cmd/$(APP_NAME) --config ../configs/example.yaml --dry-run

clean:
	rm -rf $(BIN_DIR) $(CACHE_DIR)
