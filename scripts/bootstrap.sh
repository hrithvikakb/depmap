#!/bin/bash
set -euo pipefail

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case $OS in
    darwin|linux)
        VM_NAME="depmap-build"
        ;;
    msys*|mingw*|cygwin*)
        OS="windows"
        VM_NAME="depmap-build"
        ;;
    *)
        echo "Unsupported operating system: $OS"
        exit 1
        ;;
esac

# Check if Multipass is installed
if ! command -v multipass &> /dev/null; then
    echo "Multipass is not installed. Please install it first:"
    case $OS in
        darwin)
            echo "  brew install --cask multipass"
            ;;
        windows)
            echo "  winget install Canonical.Multipass"
            ;;
        linux)
            echo "  snap install multipass"
            ;;
    esac
    exit 1
fi

# Launch build VM if it doesn't exist
if ! multipass list | grep -q "$VM_NAME"; then
    echo "Creating $VM_NAME VM..."
    multipass launch --name $VM_NAME --memory 4G --disk 10G jammy
fi

# Prepare build environment
echo "Setting up build environment..."
multipass exec $VM_NAME -- bash -c '
    # Update system
    sudo apt-get update
    sudo apt-get install -y build-essential git curl

    # Install Go
    if ! command -v go &> /dev/null; then
        curl -LO https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
        sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
        echo "export PATH=\$PATH:/usr/local/go/bin" >> ~/.bashrc
        source ~/.bashrc
    fi

    # Install LLVM and Clang
    sudo apt-get install -y \
        llvm \
        clang \
        libbpf-dev \
        linux-headers-generic \
        protobuf-compiler

    # Install protoc plugins
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

    # Clone repository if not exists
    if [ ! -d "depmap" ]; then
        git clone https://github.com/$GITHUB_REPOSITORY depmap
    fi

    cd depmap
    make deps
    make all
'

# Copy artifacts back to host
echo "Copying build artifacts..."
multipass exec $VM_NAME -- bash -c 'cd depmap && tar czf /tmp/depmap-artifacts.tar.gz bin/'
multipass transfer $VM_NAME:/tmp/depmap-artifacts.tar.gz .

# Extract artifacts
echo "Extracting artifacts..."
tar xzf depmap-artifacts.tar.gz
rm depmap-artifacts.tar.gz

# Print success message
echo "Build completed successfully!"
echo
echo "The following artifacts are now available:"
echo "  - bin/bpf/flow_observer.o (eBPF program)"
echo "  - bin/agent/agent (Agent binary)"
echo "  - bin/client/depmap (Client binary)"
echo
echo "To build Docker images, run: make image"
echo "To package Helm chart, run: make helm-chart" 