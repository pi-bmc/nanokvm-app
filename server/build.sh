#!/bin/bash

set -e

# Configuration Variables
BINARY_NAME="NanoKVM-Server"

# Define colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper function to check if a command exists
check_dependency() {
    if ! command -v "$1" &> /dev/null; then
        echo -e "${RED}[ERROR] Required command '$1' not found.${NC}"
        echo "Please install it or ensure it is in your PATH."
        exit 1
    fi
}

# ------------------------------------------------------------------------------
# Step 1: Check Prerequisites
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[INFO] Checking build environment...${NC}"

check_dependency "go"

echo -e "${GREEN}[OK] All dependencies found.${NC}"

# ------------------------------------------------------------------------------
# Step 2: Build the Binary
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[INFO] Starting cross-compilation for RISC-V 64-bit...${NC}"

export CGO_ENABLED=0
export GOOS=linux
export GOARCH=riscv64

go build -o "$BINARY_NAME" -v

if [ -f "$BINARY_NAME" ]; then
    echo -e "${GREEN}[SUCCESS] Binary '$BINARY_NAME' created successfully.${NC}"
else
    echo -e "${RED}[ERROR] Build failed. Binary not found.${NC}"
    exit 1
fi

echo -e "${GREEN}[DONE] Build script completed successfully!${NC}"
