# Build stage
FROM golang:1.24-bullseye AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y \
    clang \
    llvm \
    make \
    protobuf-compiler \
    pkg-config \
    libbpf-dev \
    bpfcc-tools \
    linux-headers-generic \
    && rm -rf /var/lib/apt/lists/*

# Install protoc plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Create symlinks for asm headers if they don't exist
RUN ln -s /usr/include/$(uname -m)-linux-gnu/asm /usr/include/asm || true && \
    ln -s /usr/include/$(uname -m)-linux-gnu/bits /usr/include/bits || true && \
    ln -s /usr/include/$(uname -m)-linux-gnu/sys /usr/include/sys || true

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build all components
RUN make clean && make all

# Runtime stage
FROM debian:bullseye-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libbpf0 \
    && rm -rf /var/lib/apt/lists/*

# Create necessary directories
RUN mkdir -p /opt/depmap/bpf /opt/cni/bin

# Copy binaries and eBPF programs
COPY --from=builder /build/bin/depmap-agent /usr/local/bin/
COPY --from=builder /build/bin/depmap-server /usr/local/bin/
COPY --from=builder /build/bin/depmap /usr/local/bin/
COPY --from=builder /build/build/flow_observer.o /opt/depmap/bpf/flow_observer.o

# Set environment variables
ENV DEPMAP_MODE=k8s
ENV DEPMAP_SERVER_PORT=50051
ENV DEPMAP_AGENT_PORT=50052
ENV BPF_OBJECT_PATH=/opt/depmap/bpf/flow_observer.o

# Default command (can be overridden)
CMD ["/usr/local/bin/depmap-agent"] 