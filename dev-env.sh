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
export DEFAULT_CASSANDRA_IMAGE="cinple/mr-cassop/cassandra:4.1.4-0.6.0"
export DEFAULT_PROBER_IMAGE="cinple/mr-cassop/prober:0.5.0"
export DEFAULT_JOLOKIA_IMAGE="cinple/mr-cassop/jolokia:0.5.0"
export DEFAULT_REAPER_IMAGE="thelastpickle/cassandra-reaper:3.2.0"
export DEFAULT_ICARUS_IMAGE="cinple/mr-cassop/icarus:0.5.0"

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