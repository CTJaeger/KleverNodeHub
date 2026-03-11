.PHONY: build-dashboard build-agent test lint clean

DASHBOARD_BIN := klever-node-hub
AGENT_BIN := klever-agent

build-dashboard:
	go build -o $(DASHBOARD_BIN) ./cmd/dashboard

build-agent:
	go build -o $(AGENT_BIN) ./cmd/agent

build: build-dashboard build-agent

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

security:
	govulncheck ./...

clean:
	rm -f $(DASHBOARD_BIN) $(AGENT_BIN)
	rm -f coverage.out
