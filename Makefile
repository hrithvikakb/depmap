# Build variables
REGISTRY ?= ghcr.io/$(shell git config --get remote.origin.url | sed 's/.*github.com\///g' | sed 's/\.git//g')
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse HEAD)
DATE ?= $(shell date -u '+%Y-%m-%d-%H:%M UTC')

# Tool versions
GO_VERSION = 1.22
CLANG_VERSION = 14

# Directories
BPF_DIR = pkg/bpf
BIN_DIR = bin
HELM_DIR = helm/hubble-mapper

# Go build flags
LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

# Default target
.PHONY: all
all: bpf agent client helm-chart

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all        : Build everything (default)"
	@echo "  bpf        : Build eBPF programs"
	@echo "  agent      : Build agent binary"
	@echo "  client     : Build client binary"
	@echo "  image      : Build Docker images"
	@echo "  helm-chart : Package Helm chart"
	@echo "  test       : Run tests"
	@echo "  clean      : Clean build artifacts"
	@echo "  proto      : Generate protobuf code"

# Build eBPF programs
.PHONY: bpf
bpf: $(BIN_DIR)/bpf/flow_observer.o

$(BIN_DIR)/bpf/flow_observer.o: $(BPF_DIR)/flow_observer.c
	@echo "Building eBPF program..."
	clang -O2 -target bpf -c $< -o $@

# Build agent binary
.PHONY: agent
agent: $(BIN_DIR)/agent/agent

$(BIN_DIR)/agent/agent: $(shell find cmd/agent -type f -name '*.go')
	@echo "Building agent..."
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agent

# Build client binary
.PHONY: client
client: $(BIN_DIR)/client/depmap

$(BIN_DIR)/client/depmap: $(shell find cmd/client -type f -name '*.go')
	@echo "Building client..."
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $@ ./cmd/client

# Build Docker images
.PHONY: image
image: agent bpf
	@echo "Building Docker images..."
	docker build -t $(REGISTRY)/depmap-agent:$(VERSION) \
		--build-arg BPF_PATH=$(BIN_DIR)/bpf/flow_observer.o \
		--build-arg AGENT_PATH=$(BIN_DIR)/agent/agent \
		-f Dockerfile .
	docker tag $(REGISTRY)/depmap-agent:$(VERSION) $(REGISTRY)/depmap-agent:latest

# Package Helm chart
.PHONY: helm-chart
helm-chart:
	@echo "Packaging Helm chart..."
	helm package $(HELM_DIR) -d $(BIN_DIR)

# Run tests
.PHONY: test
test:
	go test -v ./...

# Generate protobuf code
.PHONY: proto
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/flow/flow.proto

# Clean build artifacts
.PHONY: clean
clean:
	rm -rf $(BIN_DIR)/*
	rm -f *.o

# Install development dependencies
.PHONY: deps
deps:
	@echo "Installing development dependencies..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest 