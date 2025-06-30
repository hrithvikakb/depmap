# Build stage
FROM golang:1.24-bullseye AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y \
    clang \
    llvm \
    make \
    protobuf-compiler \
    libbpf-dev \
    && rm -rf /var/lib/apt/lists/*

# Install protoc plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Fix asm symlink issue
RUN ln -s /usr/include/$(uname -m)-linux-gnu/asm /usr/include/asm || true && \
    ln -s /usr/include/$(uname -m)-linux-gnu/bits /usr/include/bits || true && \
    ln -s /usr/include/$(uname -m)-linux-gnu/gnu /usr/include/gnu || true && \
    ln -s /usr/include/$(uname -m)-linux-gnu/sys /usr/include/sys || true

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make all

# Runtime stage
FROM debian:bullseye-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libbpf0 \
    iproute2 \
    curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/bin/depmap-agent /usr/local/bin/
COPY --from=builder /app/build/flow_observer.o /opt/depmap/bpf/

CMD ["/usr/local/bin/depmap-agent"] 