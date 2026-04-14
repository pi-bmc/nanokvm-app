# Makefile for NanoKVM Project

# Configuration
IMAGE_NAME := nanokvm-builder
UID := $(shell id -u)
GID := $(shell id -g)
PWD := $(shell pwd)

# Docker run common parameters
DOCKER_RUN_BASE := docker run --platform=linux/amd64 -e UID=$(UID) -e GID=$(GID) -v $(PWD):/home/build/NanoKVM --rm

# Build commands
GO_BUILD_CMD := cd /home/build/NanoKVM/server && go mod tidy && CGO_ENABLED=1 GOOS=linux GOARCH=riscv64 CC=riscv64-unknown-linux-musl-gcc CGO_CFLAGS="-mcpu=c906fdv -march=rv64imafdcv0p7xthead -mcmodel=medany -mabi=lp64d" go build
SUPPORT_BUILD_CMD := . ./home/build/MaixCDK/bin/activate && cd /home/build/NanoKVM/support/sg2002 && ./build kvm_system && ./build kvm_system add_to_kvmapp

VISION_BUILD_CMD := . /home/build/MaixCDK/bin/activate && cd /home/build/NanoKVM/support/sg2002 && ./build kvm_vision && ./build kvm_vision add_to_kvmapp && cp -rf /home/build/NanoKVM/support/sg2002/kvm_vision_test/dist/kvm_vision_test_release/dl_lib/* /home/build/NanoKVM/server/dl_lib/ && cp -f /home/build/MaixCDK/dl/extracted/opencv/opencv4/opencv4_lib_maixcam_musl_4.9.0/dl_lib/libopencv_video.so.4.9.0 /home/build/NanoKVM/server/dl_lib/libopencv_video.so.409

.PHONY: help check-root builder-image rebuild-image check-image shell app support vision all clean

# Default target
all: app support vision

# Help target
help:
	@echo "NanoKVM Build System"
	@echo ""
	@echo "Available targets:"
	@echo "  help          - Show this help message"
	@echo "  check-image   - Check builder Docker image and show versions"
	@echo "  builder-image - Build Docker image if not exists"
	@echo "  rebuild-image - Force rebuild Docker image"
	@echo "  shell         - Enter interactive builder environment"
	@echo "  app           - Build Go application server"
	@echo "  support       - Build hardware support libraries"
	@echo "  vision        - Build vision shared libraries (server/dl_lib)"
	@echo "  all           - Build app, support, and vision (default)"
	@echo "  clean         - Clean build artifacts"
	@echo ""
	@echo "Prerequisites:"
	@echo "  - Docker must be installed and running"
	@echo "  - Must not run as root user"

# Security check - prevent running as root
check-root:
	@if [ "$$(id -u)" -eq 0 ]; then \
		echo "Can't run as root"; \
		exit 1; \
	fi

# Check if builder image exists and show versions
check-image: check-root
	@echo "Checking builder image..."
	@echo "Golang version: " && \
		docker run --platform=linux/amd64 --rm -i $(IMAGE_NAME) go version && \
		echo "" && \
		echo "Host-tools version:" && \
		docker run --platform=linux/amd64 --rm -i $(IMAGE_NAME) riscv64-unknown-linux-musl-gcc -v && \
		echo ""

# Build Docker image if it doesn't exist
builder-image: check-root
	@if ! docker image inspect $(IMAGE_NAME) >/dev/null 2>&1; then \
		echo "Building Docker image..."; \
		docker buildx build --platform linux/amd64 -t $(IMAGE_NAME) -f docker/Dockerfile ./; \
	else \
		echo "Docker image $(IMAGE_NAME) already exists."; \
	fi

# Force rebuild Docker image
rebuild-image: check-root
	@echo "Force rebuilding Docker image..."
	@docker buildx build --platform linux/amd64 --no-cache -t $(IMAGE_NAME) -f docker/Dockerfile ./

# Enter interactive shell (equivalent to build.sh with no arguments)
shell: check-root builder-image
	@echo "Switching into builder..."
	@$(DOCKER_RUN_BASE) -it $(IMAGE_NAME) /bin/bash -c ". /home/build/MaixCDK/bin/activate && cd /home/build/NanoKVM ; exec bash"

# Build Go application
app: check-root builder-image
	@echo "Building app..."
	@$(DOCKER_RUN_BASE) -it $(IMAGE_NAME) /bin/bash -c '$(GO_BUILD_CMD)'

# Build hardware support libraries
support: check-root builder-image
	@echo "Building support..."
	@$(DOCKER_RUN_BASE) -it $(IMAGE_NAME) /bin/bash -c '$(SUPPORT_BUILD_CMD)'

# Build vision shared libraries into server/dl_lib
vision: check-root builder-image
	@echo "Building vision (dl_lib)..."
	@$(DOCKER_RUN_BASE) -it $(IMAGE_NAME) /bin/bash -c '$(VISION_BUILD_CMD)'

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@if [ -f server/NanoKVM-Server ]; then \
		rm -f server/NanoKVM-Server; \
		echo "Removed server/NanoKVM-Server"; \
	fi
	@if [ -d support/sg2002/build ]; then \
		rm -rf support/sg2002/build; \
		echo "Removed support/sg2002/build"; \
	fi
	@echo "Clean completed."