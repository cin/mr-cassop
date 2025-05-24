#!/bin/bash

# Build script for all mr-cassop Docker images
# This script builds all the Docker images needed for the mr-cassop project

set -e

# Configuration
VERSION=${VERSION:-"dev-$(git rev-parse --short HEAD)"}
REGISTRY=${REGISTRY:-"cinple/mr-cassop"}
PLATFORM=${PLATFORM:-"linux/arm64"}
MULTI_PLATFORM=${MULTI_PLATFORM:-"false"}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🚀 Building mr-cassop Docker images${NC}"
echo -e "${BLUE}📋 Configuration:${NC}"
echo -e "   Version: ${GREEN}${VERSION}${NC}"
echo -e "   Registry: ${GREEN}${REGISTRY}${NC}"
echo -e "   Platform: ${GREEN}${PLATFORM}${NC}"
echo -e "   Multi-platform: ${GREEN}${MULTI_PLATFORM}${NC}"
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

# Function to build and tag an image
build_image() {
    local name=$1
    local dockerfile=$2
    local context=$3
    local full_tag="${REGISTRY}/${name}:${VERSION}"
    local latest_tag="${REGISTRY}/${name}:latest"
    
    echo -e "${YELLOW}🔨 Building ${name}...${NC}"
    
    if [[ -f "$dockerfile" ]]; then
        if [[ "$MULTI_PLATFORM" == "true" ]]; then
            # Multi-platform build (typically for CI/CD)
            echo -e "${BLUE}   Building for multiple platforms: linux/amd64,linux/arm64${NC}"
            docker buildx build \
                --platform="linux/amd64,linux/arm64" \
                --build-arg="VERSION=${VERSION}" \
                -f "$dockerfile" \
                -t "$full_tag" \
                -t "$latest_tag" \
                --push \
                "$context"
        else
            # Single platform build (for local development)
            echo -e "${BLUE}   Building for platform: ${PLATFORM}${NC}"
            docker buildx build \
                --platform="${PLATFORM}" \
                --build-arg="VERSION=${VERSION}" \
                -f "$dockerfile" \
                -t "$full_tag" \
                -t "$latest_tag" \
                --load \
                "$context"
        fi
        
        echo -e "${GREEN}✅ Built ${full_tag}${NC}"
        echo -e "${GREEN}✅ Tagged ${latest_tag}${NC}"
    else
        echo -e "${RED}❌ Dockerfile not found: ${dockerfile}${NC}"
        return 1
    fi
}

# Build main operator
echo -e "${BLUE}🔨 Building main operator...${NC}"
build_image "mr-cassop" "Dockerfile" "."

# Build prober
echo -e "${BLUE}🔨 Building prober...${NC}"
build_image "prober" "prober/Dockerfile" "prober"

# Build cassandra
echo -e "${BLUE}🔨 Building cassandra...${NC}"
build_image "cassandra" "cassandra/Dockerfile" "cassandra"

# Build jolokia
echo -e "${BLUE}🔨 Building jolokia...${NC}"
build_image "jolokia" "jolokia/Dockerfile" "jolokia"

# Build icarus
echo -e "${BLUE}🔨 Building icarus...${NC}"
build_image "icarus" "icarus/Dockerfile" "icarus"

echo ""
echo -e "${GREEN}🎉 All images built successfully!${NC}"
echo ""
echo -e "${BLUE}📋 Built images:${NC}"
docker images | grep "${REGISTRY}" | head -10

echo ""
if [[ "$MULTI_PLATFORM" == "true" ]]; then
    echo -e "${GREEN}📤 Multi-platform images pushed to registry${NC}"
else
    echo -e "${BLUE}🔧 To push images to registry (multi-platform):${NC}"
    echo -e "   MULTI_PLATFORM=true ./build-images.sh"
    echo ""
    echo -e "${BLUE}🔧 Or push individual images:${NC}"
    echo -e "   docker push ${REGISTRY}/mr-cassop:${VERSION}"
    echo -e "   docker push ${REGISTRY}/prober:${VERSION}"
    echo -e "   docker push ${REGISTRY}/cassandra:${VERSION}"
    echo -e "   docker push ${REGISTRY}/jolokia:${VERSION}"
    echo -e "   docker push ${REGISTRY}/icarus:${VERSION}"
fi
echo ""
echo -e "${BLUE}🔧 To update dev-env.sh with new images:${NC}"
echo -e "   export DEFAULT_CASSANDRA_IMAGE='${REGISTRY}/cassandra:${VERSION}'"
echo -e "   export DEFAULT_PROBER_IMAGE='${REGISTRY}/prober:${VERSION}'"
echo -e "   export DEFAULT_JOLOKIA_IMAGE='${REGISTRY}/jolokia:${VERSION}'"
echo -e "   export DEFAULT_ICARUS_IMAGE='${REGISTRY}/icarus:${VERSION}'" 