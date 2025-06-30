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
BUILD_DIR := build
INSTALL_DIR := /usr/local/bin
DEPMAP_DIR := /opt/depmap

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod

# Version information
VERSION := $(shell git describe --tags --always --dirty)
COMMIT := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)

# Check if we're on macOS
UNAME_S := $(shell uname -s)

ifeq ($(UNAME_S),Darwin)
    # On macOS, skip eBPF compilation
    all: deps build-proto build-go
else
    # On Linux, include eBPF compilation
    all: deps build-proto build-ebpf build-go
endif

# Check and install dependencies
deps:
	@echo "Checking dependencies..."
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

# Build eBPF program (Linux only)
build-ebpf:
	@echo "Building eBPF program..."
	@mkdir -p $(BUILD_DIR)
	$(CLANG) -O2 -g -Wall -Werror -target bpf -D__TARGET_ARCH_x86 -c $(BPF_DIR)/flow_observer.c -o $(BUILD_DIR)/flow_observer.o

# Build Go binaries
build-go:
	@echo "Building Go binaries..."
	@mkdir -p $(BIN_DIR)
ifeq ($(UNAME_S),Linux)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/depmap-agent ./cmd/agent
endif
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/depmap-server ./cmd/server
	$(GOBUILD) -o $(BIN_DIR)/depmap ./cmd/client

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BIN_DIR)
	rm -rf $(BUILD_DIR)
	rm -f $(GO_OUT_DIR)/flow/*.pb.go

# Install binaries and configs
install: all
	@echo "Installing depmap..."
	install -d $(INSTALL_DIR)
	install -d $(DEPMAP_DIR)/bpf
	install -m 755 $(BIN_DIR)/depmap $(INSTALL_DIR)/
	install -m 755 $(BIN_DIR)/depmap-agent $(INSTALL_DIR)/
	install -m 755 $(BIN_DIR)/depmap-server $(INSTALL_DIR)/
	install -m 644 $(BPF_DIR)/flow_observer.o $(DEPMAP_DIR)/bpf/
	@echo "Installation complete!"

# Uninstall binaries and configs
uninstall:
	@echo "Uninstalling depmap..."
	rm -f $(INSTALL_DIR)/depmap
	rm -f $(INSTALL_DIR)/depmap-agent
	rm -f $(INSTALL_DIR)/depmap-server
	rm -f $(DEPMAP_DIR)/bpf/flow_observer.o
	rm -rf $(DEPMAP_DIR)
	@echo "Uninstallation complete!"

# Run targets
.PHONY: run-agent run-server run-client

run-agent: build
	@echo "Running agent..."
	sudo ./$(BIN_DIR)/depmap-agent

run-server: build
	@echo "Running server..."
	./$(BIN_DIR)/depmap-server

run-client: build
	@echo "Running client..."
	./$(BIN_DIR)/depmap

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