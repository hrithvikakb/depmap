# Depmap - Kubernetes Service Dependency Mapper

Depmap is a lightweight service dependency mapping tool for Kubernetes that uses eBPF to track and visualize service-to-service communication in real-time. It's designed as a minimal alternative to Cilium + Hubble, focusing solely on service dependency mapping.

## Features

- Real-time service dependency tracking using eBPF
- Kubernetes-aware pod mapping
- Support for TCP and UDP protocols
- Tracks both successful (FORWARDED) and failed (DROPPED) connections
- Simple JSON output format for easy integration
- Cross-platform support (macOS, Windows, Linux)

## Quick Start

### Option 1: Homebrew (macOS)

```bash
# Install via Homebrew
brew install depmap

# Install in your cluster
depmap install

# Start observing traffic
depmap observe
```

### Option 2: One-liner Install Script

```bash
# Download and install depmap
curl -sSfL https://raw.githubusercontent.com/yourusername/depmap/main/install.sh | bash

# Install in your cluster
depmap install

# Start observing traffic
depmap observe
```

### Option 3: Manual Installation

1. Download the latest release from [GitHub Releases](https://github.com/yourusername/depmap/releases)
2. Extract and move the binary to your PATH
3. Run `depmap install` to deploy to your cluster

## Prerequisites

- Kubernetes cluster (tested with kind, k3d, microk8s, EKS, GKE)
- For GKE: requires Standard cluster with `--network-plugin=none`
- kubectl configured with cluster access
- For local development:
  - Go 1.22+
  - Clang and LLVM (for eBPF compilation)
  - Docker for building images

## Development

1. Clone the repository:
```bash
git clone https://github.com/yourusername/depmap
cd depmap
```

2. Build all components:
```bash
make all
```

Available make targets:
- `make bpf`: Build eBPF programs
- `make agent`: Build agent binary
- `make image`: Build Docker images
- `make helm-chart`: Package Helm chart
- `make test`: Run tests
- `make clean`: Clean build artifacts

3. For development builds on macOS/Windows:
```bash
./scripts/bootstrap.sh
```
This will:
- Create a Multipass VM for building
- Install required dependencies
- Build all components
- Copy artifacts back to host

## Architecture

Depmap consists of three main components:

1. **eBPF Program**
   - Hooks into pod interfaces
   - Captures L3/L4 flow information
   - Emits events to userspace

2. **Agent (DaemonSet)**
   - Runs on every node
   - Loads eBPF programs
   - Collects flow data
   - Forwards to relay

3. **Relay (Deployment)**
   - Aggregates flow data
   - Provides gRPC API
   - Serves `depmap observe` requests
