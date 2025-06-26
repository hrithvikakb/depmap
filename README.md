# Depmap - Kubernetes Service Dependency Mapper

A lightweight service dependency mapper for Kubernetes that shows live service-to-service communication patterns using eBPF.

## Features

- **Live Flow Monitoring**: Captures and streams network flows between pods in real-time
- **Kubernetes-Aware**: Maps IP addresses to pod names, namespaces, and workload information
- **Protocol Detection**: Identifies L4 protocols (TCP/UDP) for each flow
- **Verdict Tracking**: Shows both successful (FORWARDED) and failed (DROPPED) connection attempts
- **Filtering**: Filter flows by namespace or verdict
- **Multiple Output Formats**: JSON and human-readable text output

## Prerequisites

- Linux kernel 4.18+ (for eBPF support)
- Go 1.19+
- LLVM/Clang (for compiling eBPF programs)
- Protocol Buffers compiler (protoc)
- Access to a Kubernetes cluster (local or remote)

## Installation

We have three binaries:
- `depmap-agent`: The eBPF-based flow collector
- `depmap-server`: The gRPC server that streams flows
- `depmap`: The CLI client for observing flows

## Usage

1. Start the agent (requires root privileges):
   ```bash
   sudo depmap-agent
   ```

2. In another terminal, start the server:
   ```bash
   depmap-server
   ```

3. In a third terminal, observe flows:
   ```bash
   # Watch all flows in JSON format
   depmap --format json

   # Filter flows by namespace
   depmap --namespace default

   # Filter by verdict
   depmap --verdict DROPPED

   # Human-readable output
   depmap --format text
   ```

Example output (text format):
```
frontend (default) -> productcatalog (default) [TCP] (FORWARDED)
frontend (default) -> cart (default) [TCP] (DROPPED)
```

## Architecture

The system consists of three main components:

1. **Agent**:
   - Loads and attaches eBPF program to network interfaces
   - Reads flow events from eBPF maps
   - Enriches flows with Kubernetes metadata
   - Forwards flows to the gRPC server

2. **Server**:
   - Receives flows from the agent
   - Implements flow filtering
   - Streams flows to connected clients

3. **Client**:
   - Connects to the server via gRPC
   - Applies filters (namespace, verdict)
   - Formats and displays flows
