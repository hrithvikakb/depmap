#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${BLUE}Starting build process...${NC}"

# Check required tools
echo -e "${BLUE}Checking build dependencies...${NC}"

# Check Go
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed${NC}"
    echo "Please install Go 1.21 or later from https://golang.org/dl/"
    exit 1
fi

# Check protoc
if ! command -v protoc &> /dev/null; then
    echo -e "${RED}Error: Protocol Buffers compiler is not installed${NC}"
    echo "Please install protoc from https://github.com/protocolbuffers/protobuf/releases"
    exit 1
fi

# Check LLVM/Clang for eBPF
if ! command -v clang &> /dev/null; then
    echo -e "${RED}Error: Clang is not installed${NC}"
    echo "Please install LLVM/Clang for eBPF compilation"
    exit 1
fi

# Download Go dependencies
echo -e "${BLUE}Downloading Go dependencies...${NC}"
go mod download

# Generate protobuf files
echo -e "${BLUE}Generating protobuf files...${NC}"
mkdir -p pkg/proto/flow
protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    proto/flow/flow.proto

# Build eBPF program
echo -e "${BLUE}Building eBPF program...${NC}"
clang -O2 -target bpf -c pkg/bpf/flow_observer.c -o pkg/bpf/flow_observer.o

# Build Go binaries
echo -e "${BLUE}Building Go binaries...${NC}"
mkdir -p bin

echo "Building CNI plugin..."
go build -o bin/cni cmd/cni/main.go

echo "Building flow agent..."
go build -o bin/agent cmd/agent/main.go

echo "Building gRPC server..."
go build -o bin/server cmd/server/main.go

echo "Building client..."
go build -o bin/client cmd/client/main.go

echo -e "${GREEN}Build completed successfully!${NC}"
echo ""
echo -e "${BLUE}Generated artifacts:${NC}"
echo "  - bin/cni:    CNI plugin binary"
echo "  - bin/agent:  Flow observation agent"
echo "  - bin/server: gRPC server"
echo "  - bin/client: Service map generator client"
echo "  - pkg/bpf/flow_observer.o: Compiled eBPF program"
echo ""
echo -e "${BLUE}Next steps:${NC}"
echo "1. Run './install.sh' to deploy to Kubernetes"
echo "2. Or use individual binaries directly for testing" 