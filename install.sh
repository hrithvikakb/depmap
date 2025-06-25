#!/bin/bash
set -euo pipefail

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="x86_64" ;;
    aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Latest version by default
VERSION=${1:-$(curl -s https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/latest | grep '"tag_name":' | cut -d'"' -f4)}

# Download URL
BINARY="depmap_${OS}_${ARCH}"
if [ "$OS" = "windows" ]; then
    BINARY="${BINARY}.zip"
else
    BINARY="${BINARY}.tar.gz"
fi

URL="https://github.com/${GITHUB_REPOSITORY}/releases/download/${VERSION}/${BINARY}"

# Create temporary directory
TMP_DIR=$(mktemp -d)
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

# Download and extract
echo "Downloading depmap ${VERSION}..."
curl -L "$URL" -o "$TMP_DIR/$BINARY"

cd "$TMP_DIR"
if [[ "$BINARY" == *.zip ]]; then
    unzip "$BINARY"
else
    tar xzf "$BINARY"
fi

# Install binary
echo "Installing depmap..."
INSTALL_DIR="/usr/local/bin"
if [ "$OS" = "darwin" ]; then
    sudo mkdir -p "$INSTALL_DIR"
    sudo mv depmap "$INSTALL_DIR/"
elif [ "$OS" = "linux" ]; then
    sudo mv depmap "$INSTALL_DIR/"
else
    # Windows - install to Program Files
    INSTALL_DIR="/c/Program Files/depmap"
    mkdir -p "$INSTALL_DIR"
    mv depmap.exe "$INSTALL_DIR/"
    # Add to PATH if not already there
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        echo "Adding $INSTALL_DIR to PATH..."
        setx PATH "%PATH%;$INSTALL_DIR"
    fi
fi

echo "depmap ${VERSION} installed successfully!"
echo
echo "To get started:"
echo "  1. Ensure your kubeconfig is configured correctly"
echo "  2. Run: depmap install"
echo "  3. Once installed, run: depmap observe" 