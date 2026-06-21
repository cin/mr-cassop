#!/bin/bash

# Development environment setup for mr-cassop
# This script sets the required environment variables for running the operator locally

# Operator configuration
export NAMESPACE="cassop"
export LEADERELECTION_ENABLED="false"  # Disable for local development
export LOGLEVEL="debug"
export LOGFORMAT="console"  # Better for local development
export WEBHOOKS_ENABLED="false"  # Disable for local testing
export WEBHOOKS_PORT="9443"
export METRICS_PORT="8329"
export RETRY_DELAY="10s"

# Default images - using the values from values.yaml
export DEFAULT_CASSANDRA_IMAGE="ghcr.io/cin/mr-cassop/cassandra:4.1.11-0.6.0"
export DEFAULT_PROBER_IMAGE="ghcr.io/cin/mr-cassop/prober:0.6.0"
export DEFAULT_JOLOKIA_IMAGE="ghcr.io/cin/mr-cassop/jolokia:0.6.0"
export DEFAULT_REAPER_IMAGE="thelastpickle/cassandra-reaper:4.2.5"
export DEFAULT_ICARUS_IMAGE="ghcr.io/cin/mr-cassop/icarus:0.6.0"

echo "✅ Environment variables set for local development"
echo "📋 Key settings:"
echo "   - Leader election: $LEADERELECTION_ENABLED"
echo "   - Webhooks: $WEBHOOKS_ENABLED"
echo "   - Log level: $LOGLEVEL"
echo "   - Log format: $LOGFORMAT"
echo ""
echo "🔧 To use these settings, run:"
echo "   source ./dev-env.sh"
echo "   make manager && ./bin/manager" 