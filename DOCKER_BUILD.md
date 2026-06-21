# Docker Build Setup for mr-cassop

This project supports Docker Buildx with multi-platform builds for development and production deployment across different architectures.

## Quick Start by Platform

### 🐧 **Linux AMD64 (Most Common)**

```bash
# Core images for rapid development
PLATFORM=linux/amd64 ./build-local.sh

# Essential images including Cassandra
PLATFORM=linux/amd64 ./build-local.sh --cassandra

# Or use native platform detection (usually AMD64 on Linux)
./build-local.sh --cassandra
```

### 🍎 **Apple Silicon (ARM64)**

```bash
# Core images (uses native platform detection, usually linux/arm64 on Apple Silicon)
./build-local.sh

# Essential images including Cassandra  
./build-local.sh --cassandra

# For compatibility testing with AMD64
PLATFORM=linux/amd64 ./build-local.sh
```

### 🪟 **Windows with Docker Desktop**

```bash
# Usually AMD64, but check your Docker Desktop settings
PLATFORM=linux/amd64 ./build-local.sh --cassandra
```

### 🚀 **CI/CD and Production**

```bash
# Build for both AMD64 and ARM64 (recommended for production)
MULTI_PLATFORM=true ./build-images.sh

# Or target specific production platform
PLATFORM=linux/amd64 REGISTRY=your-registry.com/mr-cassop ./build-images.sh
```

## Platform Detection and Defaults

The build system automatically detects your platform, but you can override:

| Environment | Default Platform | Override Example |
|-------------|------------------|------------------|
| **Linux AMD64** | `linux/amd64` | `PLATFORM=linux/arm64` |
| **Apple Silicon** | `linux/arm64` | `PLATFORM=linux/amd64` |
| **Cloud CI/CD** | `linux/amd64` | `MULTI_PLATFORM=true` |
| **Production** | Multi-platform | `PLATFORM=linux/amd64` |

## Available Images

The project builds 5 Docker images for Kubernetes deployment:

1. **mr-cassop** - Main Kubernetes operator
2. **prober** - Health monitoring component  
3. **cassandra** - Enhanced Cassandra image with monitoring
4. **jolokia** - JMX monitoring proxy
5. **icarus** - Backup/restore component

## Build Scripts

### `./build-local.sh`
- **Purpose**: Flexible local development builds
- **Options**: Core images (default) or essential images (`--cassandra`)
- **Platform**: Single platform (auto-detected or specified)
- **Use Cases**: 
  - `./build-local.sh` - Fastest iteration (operator + prober)
  - `./build-local.sh --cassandra` - Complete local development (+ cassandra)

### `./build-images.sh`
- **Purpose**: All 5 images with full configuration options
- **Platform**: Single or multi-platform via `MULTI_PLATFORM=true`
- **Use Case**: Complete builds, CI/CD, production releases

## Makefile Targets

### Individual Image Builds
```bash
make docker-build-operator    # Build operator image
make docker-build-prober      # Build prober image  
make docker-build-cassandra   # Build cassandra image
make docker-build-jolokia     # Build jolokia image
make docker-build-icarus      # Build icarus image
```

### Batch Operations
```bash
make docker-build-core                  # Build core images (operator + prober)
make docker-build-essential             # Build essential images (+ cassandra)
make docker-build-monitoring            # Build monitoring images (jolokia + icarus)
make docker-build-all                   # Build all images (single platform)
make docker-build-all-multiplatform     # Build for AMD64+ARM64 and push
```

### Help
```bash
make docker-help                         # Show all Docker build options
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PLATFORM` | Auto-detected | Target platform: `linux/amd64`, `linux/arm64` |
| `MULTI_PLATFORM` | `false` | Enable multi-platform builds (AMD64+ARM64) |
| `REGISTRY` | `ghcr.io/cin/mr-cassop` | Docker registry prefix |
| `VERSION` | `dev-<git-hash>` | Image version tag |

### Platform-Specific Examples

```bash
# Linux AMD64 (most CI/CD and cloud environments)
PLATFORM=linux/amd64 ./build-local.sh --cassandra

# Apple Silicon development
PLATFORM=linux/arm64 ./build-local.sh --cassandra

# Multi-platform for production
MULTI_PLATFORM=true REGISTRY=your-registry.com/mr-cassop ./build-images.sh

# Specific version for release
VERSION=v1.2.3 PLATFORM=linux/amd64 ./build-images.sh
```

## Common Deployment Scenarios

### 🏗️ **Local Development**
```bash
# Quick core setup (fastest)
./build-local.sh

# Complete setup with Cassandra
./build-local.sh --cassandra

# Cross-platform testing
PLATFORM=linux/amd64 ./build-local.sh
PLATFORM=linux/arm64 ./build-local.sh --cassandra
```

### 🏭 **CI/CD Pipelines**
```bash
# Most CI systems (GitHub Actions, GitLab CI, Jenkins)
PLATFORM=linux/amd64 REGISTRY=$CI_REGISTRY ./build-images.sh

# Multi-platform release builds
MULTI_PLATFORM=true REGISTRY=$REGISTRY VERSION=$CI_COMMIT_TAG ./build-images.sh
```

### ☁️ **Cloud Deployment**
```bash
# AWS, GCP, Azure (typically AMD64)
PLATFORM=linux/amd64 REGISTRY=your-registry ./build-images.sh

# ARM64 cloud instances (AWS Graviton, etc.)
PLATFORM=linux/arm64 REGISTRY=your-registry ./build-images.sh
```

### 🎯 **Production Releases**
```bash
# Build for both architectures
MULTI_PLATFORM=true REGISTRY=production-registry.com VERSION=v1.0.0 ./build-images.sh
```

## Docker Buildx Setup

The build system automatically creates a Docker Buildx builder for cross-platform support:

```bash
# Automatic setup (done by build scripts)
docker buildx create --name mr-cassop-builder --use --bootstrap

# Manual verification
docker buildx inspect mr-cassop-builder
```

## Development Workflow by Environment

### 🖥️ **Local Development Workflow**
1. **Fast iteration**: `./build-local.sh` (core components only)
2. **Full environment**: `./build-local.sh --cassandra` (includes Cassandra)
3. **Complete testing**: `make docker-build-all`

### 🔄 **CI/CD Workflow**
1. **Build**: `PLATFORM=linux/amd64 ./build-images.sh`
2. **Test**: Deploy to test cluster
3. **Release**: `MULTI_PLATFORM=true ./build-images.sh`

### 🚀 **Production Workflow**
1. **Tag release**: `VERSION=v1.x.x MULTI_PLATFORM=true ./build-images.sh`
2. **Deploy**: Use multi-platform images in Kubernetes
3. **Verify**: Platform-specific deployment validation

## Performance Notes by Platform

| Platform | Build Speed | Use Case | Notes |
|----------|-------------|----------|-------|
| **Linux AMD64** | Fast (native) | CI/CD, Production | Most common, best compatibility |
| **Apple Silicon** | Very Fast (native ARM64) | Development | 2-3x faster than emulated AMD64 |
| **Cross-platform** | Slower | Production releases | Builds both AMD64 and ARM64 |
| **Emulated** | Slow | Testing only | Use sparingly |

## Troubleshooting

### Platform Detection Issues
```bash
# Check your Docker platform
docker version --format '{{.Server.Arch}}'

# Force specific platform
PLATFORM=linux/amd64 ./build-local.sh
```

### Buildx Not Available
```bash
# Install/enable buildx
docker buildx create --name mr-cassop-builder --use --bootstrap
```

### Multi-platform Build Fails
```bash
# Check buildx support
docker buildx ls

# Verify qemu emulation (for cross-platform)
docker run --rm --privileged multiarch/qemu-user-static --reset -p yes
```

### Performance Issues
- **Slow builds**: Use single platform for development (`PLATFORM=linux/amd64`)
- **Memory issues**: Increase Docker memory limits (Docker Desktop)
- **Disk space**: Use `docker system prune` to clean up build cache 