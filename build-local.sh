#!/bin/bash

# Local build script for mr-cassop images
# Supports different image sets for various development needs

set -e

# Configuration
VERSION=${VERSION:-"dev-$(git rev-parse --short HEAD)"}
REGISTRY=${REGISTRY:-"cinple/mr-cassop"}
PLATFORM=${PLATFORM:-"linux/arm64"}

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Parse command line arguments
CASSANDRA=false
HELP=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --cassandra|-c)
            CASSANDRA=true
            shift
            ;;
        --help|-h)
            HELP=true
            shift
            ;;
        *)
            echo -e "${RED}❌ Unknown option: $1${NC}"
            HELP=true
            shift
            ;;
    esac
done

# Show help
if [[ "$HELP" == "true" ]]; then
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Local build script for mr-cassop development images"
    echo ""
    echo "Options:"
    echo "  -c, --cassandra    Include Cassandra image (operator + prober + cassandra)"
    echo "  -h, --help         Show this help message"
    echo ""
    echo "Default: Builds core images (operator + prober) only"
    echo ""
    echo "Examples:"
    echo "  $0                 # Build core images (fast)"
    echo "  $0 --cassandra     # Build essential images (complete local dev)"
    echo ""
    echo "Environment Variables:"
    echo "  PLATFORM=linux/amd64   # Override target platform"
    echo "  VERSION=v1.0.0         # Override version tag"  
    echo "  REGISTRY=my-registry    # Override registry"
    exit 0
fi

# Determine what to build
if [[ "$CASSANDRA" == "true" ]]; then
    BUILD_TYPE="essential"
    BUILD_DESC="operator + prober + cassandra"
    IMAGES=("operator" "prober" "cassandra")
else
    BUILD_TYPE="core"
    BUILD_DESC="operator + prober"
    IMAGES=("operator" "prober")
fi

echo -e "${BLUE}🚀 Building ${BUILD_TYPE} mr-cassop images for local development${NC}"
echo -e "${BLUE}📋 Configuration:${NC}"
echo -e "   Version: ${GREEN}${VERSION}${NC}"
echo -e "   Registry: ${GREEN}${REGISTRY}${NC}"
echo -e "   Platform: ${GREEN}${PLATFORM}${NC}"
echo -e "   Images: ${GREEN}${BUILD_DESC}${NC}"
echo ""

# Check if buildx is available and create builder if needed
if ! docker buildx inspect mr-cassop-builder > /dev/null 2>&1; then
    echo -e "${YELLOW}📦 Creating buildx builder 'mr-cassop-builder'...${NC}"
    docker buildx create --name mr-cassop-builder --use --bootstrap
else
    echo -e "${GREEN}✅ Using existing buildx builder 'mr-cassop-builder'${NC}"
    docker buildx use mr-cassop-builder
fi
echo ""

# Build counter
CURRENT=1
TOTAL=${#IMAGES[@]}

# Build each image
for image in "${IMAGES[@]}"; do
    echo -e "${YELLOW}🔨 Building ${image} (${CURRENT}/${TOTAL})...${NC}"
    
    case $image in
        operator)
            echo -e "${BLUE}   Building operator binary first...${NC}"
            make manager
            echo -e "${BLUE}   Building Docker image for platform: ${PLATFORM}${NC}"
            docker buildx build \
                --platform="${PLATFORM}" \
                --build-arg="VERSION=${VERSION}" \
                -t "${REGISTRY}/mr-cassop:${VERSION}" \
                -t "${REGISTRY}/mr-cassop:latest" \
                --load \
                .
            ;;
        prober)
            echo -e "${BLUE}   Building Docker image for platform: ${PLATFORM}${NC}"
            docker buildx build \
                --platform="${PLATFORM}" \
                --build-arg="VERSION=${VERSION}" \
                -f prober/Dockerfile \
                -t "${REGISTRY}/prober:${VERSION}" \
                -t "${REGISTRY}/prober:latest" \
                --load \
                prober
            ;;
        cassandra)
            echo -e "${BLUE}   Building Docker image for platform: ${PLATFORM} (this may take a while)${NC}"
            docker buildx build \
                --platform="${PLATFORM}" \
                -t "${REGISTRY}/cassandra:${VERSION}" \
                -t "${REGISTRY}/cassandra:latest" \
                --load \
                cassandra
            ;;
    esac
    
    echo -e "${GREEN}✅ Built ${image} image${NC}"
    echo ""
    
    ((CURRENT++))
done

echo -e "${GREEN}🎉 ${BUILD_TYPE^} images built successfully!${NC}"
echo ""
echo -e "${BLUE}📋 Built images:${NC}"
if [[ "$CASSANDRA" == "true" ]]; then
    docker images | grep "${REGISTRY}" | grep -E "(mr-cassop|prober|cassandra)" | head -6
else
    docker images | grep "${REGISTRY}" | grep -E "(mr-cassop|prober)" | head -4
fi

echo ""
echo -e "${BLUE}🔧 To update dev-env.sh with new images:${NC}"
echo -e "   export DEFAULT_PROBER_IMAGE='${REGISTRY}/prober:${VERSION}'"
if [[ "$CASSANDRA" == "true" ]]; then
    echo -e "   export DEFAULT_CASSANDRA_IMAGE='${REGISTRY}/cassandra:${VERSION}'"
fi

echo ""
if [[ "$CASSANDRA" != "true" ]]; then
    echo -e "${BLUE}🔧 To build with Cassandra:${NC}"
    echo -e "   $0 --cassandra"
    echo ""
fi

echo -e "${BLUE}🔧 To build monitoring images (jolokia + icarus):${NC}"
echo -e "   make docker-build-jolokia"
echo -e "   make docker-build-icarus"
echo ""
echo -e "${BLUE}🔧 To build all images:${NC}"
echo -e "   ./build-images.sh" 