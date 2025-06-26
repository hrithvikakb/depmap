# Build targets
.PHONY: all clean deps build-proto build-ebpf build-go test install uninstall

# Variables
SHELL := /bin/bash
PROTOC := protoc
GO := go
CLANG := clang
LLC := llc

# Directories
BPF_DIR := pkg/bpf
PROTO_DIR := proto
GO_OUT_DIR := pkg/proto
BIN_DIR := bin
INSTALL_DIR := /usr/local/bin
DEPMAP_DIR := /opt/depmap

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod

# eBPF parameters
CFLAGS := -O2 -g -Wall -Werror $(CFLAGS)
BPF_CFLAGS := $(CFLAGS) -target bpf -D__TARGET_ARCH_x86

# Binary names
BINARY_NAME := depmap
BPF_BINARY := flow_observer.o
AGENT_BINARY := depmap-agent
SERVER_BINARY := depmap-server
CLIENT_BINARY := depmap

# Version information
VERSION := $(shell git describe --tags --always --dirty)
COMMIT := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)

# Build directory
BUILD_DIR := build

# eBPF source and object files
BPF_SRC := $(BPF_DIR)/flow_observer.c
BPF_OBJ := $(BUILD_DIR)/$(BPF_BINARY)

all: deps build-proto build-ebpf build-go

# Check and install dependencies
deps:
	@echo "Checking dependencies..."
	@command -v $(CLANG) >/dev/null 2>&1 || { echo "clang is required but not installed"; exit 1; }
	@command -v $(GO) >/dev/null 2>&1 || { echo "go is required but not installed"; exit 1; }
	@command -v $(PROTOC) >/dev/null 2>&1 || { echo "protoc is required but not installed"; exit 1; }
	@$(GOMOD) download
	@$(GOMOD) verify

# Build protobuf files
build-proto:
	@echo "Building protocol buffers..."
	@mkdir -p $(GO_OUT_DIR)/flow
	$(PROTOC) --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/flow/flow.proto

# Build eBPF program
build-ebpf:
	@echo "Building eBPF program..."
	@mkdir -p $(BUILD_DIR)
	$(CLANG) $(BPF_CFLAGS) -c $(BPF_SRC) -o $(BPF_OBJ)

# Build Go binaries
build-go:
	@echo "Building Go binaries..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(AGENT_BINARY) ./cmd/agent
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(SERVER_BINARY) ./cmd/server
	$(GOBUILD) -o $(BIN_DIR)/$(CLIENT_BINARY) ./cmd/client

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BIN_DIR)
	rm -f $(BPF_DIR)/$(BPF_BINARY)
	rm -f $(GO_OUT_DIR)/flow/*.pb.go

# Install binaries and configs
install: all
	@echo "Installing depmap..."
	install -d $(INSTALL_DIR)
	install -d $(DEPMAP_DIR)/bpf
	install -m 755 $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/
	install -m 755 $(BIN_DIR)/$(AGENT_BINARY) $(INSTALL_DIR)/
	install -m 755 $(BIN_DIR)/$(SERVER_BINARY) $(INSTALL_DIR)/
	install -m 755 $(BIN_DIR)/$(CLIENT_BINARY) $(INSTALL_DIR)/
	install -m 644 $(BPF_DIR)/$(BPF_BINARY) $(DEPMAP_DIR)/bpf/
	@echo "Installation complete!"

# Uninstall binaries and configs
uninstall:
	@echo "Uninstalling depmap..."
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	rm -f $(INSTALL_DIR)/$(AGENT_BINARY)
	rm -f $(INSTALL_DIR)/$(SERVER_BINARY)
	rm -f $(INSTALL_DIR)/$(CLIENT_BINARY)
	rm -f $(DEPMAP_DIR)/bpf/$(BPF_BINARY)
	rm -rf $(DEPMAP_DIR)
	@echo "Uninstallation complete!"

# Run targets
.PHONY: run-agent run-server run-client

run-agent: build
	@echo "Running agent..."
	sudo ./$(BIN_DIR)/$(AGENT_BINARY)

run-server: build
	@echo "Running server..."
	./$(BIN_DIR)/$(SERVER_BINARY)

run-client: build
	@echo "Running client..."
	./$(BIN_DIR)/$(CLIENT_BINARY)

# Docker image targets
.PHONY: image load

# Docker image name and tag
IMAGE_NAME ?= depmap
IMAGE_TAG ?= latest

image:
	@echo "Building Docker image $(IMAGE_NAME):$(IMAGE_TAG)..."
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

load: image
	@echo "Loading image into k3d cluster..."
	@if k3d cluster list | grep -q "depmap-demo"; then \
		k3d image import $(IMAGE_NAME):$(IMAGE_TAG) -c depmap-demo; \
	else \
		echo "Cluster depmap-demo not found. Please create it first."; \
		exit 1; \
	fi 