APP_NAME := pgdrivebackup
APP_DIR := pgdrivebackup
BIN_DIR := bin
BIN_PATH := $(BIN_DIR)/$(APP_NAME)
CACHE_DIR := .cache
GO_ENV := GOCACHE=$(abspath $(CACHE_DIR)/go-build) GOMODCACHE=$(abspath $(CACHE_DIR)/go-mod)

.PHONY: build test run dry-run clean

build:
	mkdir -p $(BIN_DIR)
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go build -o ../$(BIN_PATH) ./cmd/$(APP_NAME)

test:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go test ./...

run:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go run ./cmd/$(APP_NAME) --config configs/example.yaml

dry-run:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod
	cd $(APP_DIR) && $(GO_ENV) go run ./cmd/$(APP_NAME) --config configs/example.yaml --dry-run

clean:
	rm -rf $(BIN_DIR) $(CACHE_DIR)
