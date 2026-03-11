.PHONY: build-dashboard build-agent build test lint security coverage clean

BIN_DIR := bin
DASHBOARD_BIN := $(BIN_DIR)/klever-node-hub
AGENT_BIN := $(BIN_DIR)/klever-agent

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -s -w \
	-X github.com/CTJaeger/KleverNodeHub/internal/version.Version=$(VERSION) \
	-X github.com/CTJaeger/KleverNodeHub/internal/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/CTJaeger/KleverNodeHub/internal/version.BuildTime=$(BUILD_TIME)

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build-dashboard: $(BIN_DIR)
	go build -ldflags="$(LDFLAGS)" -o $(DASHBOARD_BIN) ./cmd/dashboard

build-agent: $(BIN_DIR)
	go build -ldflags="$(LDFLAGS)" -o $(AGENT_BIN) ./cmd/agent

build: build-dashboard build-agent

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...
	go vet ./...

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

security:
	govulncheck ./...

clean:
	rm -rf $(BIN_DIR)
	rm -f coverage.out
