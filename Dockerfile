# Build stage for eBPF programs
FROM ubuntu:22.04 as bpf-builder
RUN apt-get update && apt-get install -y \
    clang \
    llvm \
    libbpf-dev \
    linux-headers-generic \
    make \
    && rm -rf /var/lib/apt/lists/*

# Runtime stage
FROM alpine:3.18
RUN apk add --no-cache libbpf

# Copy binaries and eBPF programs
ARG BPF_PATH
ARG AGENT_PATH
COPY ${BPF_PATH} /opt/depmap/bpf/flow_observer.o
COPY ${AGENT_PATH} /usr/local/bin/agent

# Set up runtime environment
ENV DEPMAP_BPF_DIR=/opt/depmap/bpf
ENTRYPOINT ["/usr/local/bin/agent"] 