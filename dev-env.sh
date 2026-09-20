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
# Cassandra is on 5.0.9 as of main, but no published release has built that image yet
# (latest published tag, 0.6.6, is still Cassandra 4.1.12) - point cassandra and prober
# at main-built dev tags until a 5.0.9-based release ships, e.g. via
# `VERSION=dev ./build-local.sh --cassandra`. Prober must track cassandra here: its
# gossip parser was updated for Cassandra 5.0's INDEX_STATUS app-state (#141/#142),
# so the published prober:0.6.0 build predates 5.0 support.
export DEFAULT_CASSANDRA_IMAGE="ghcr.io/cin/mr-cassop/cassandra:dev"
export DEFAULT_PROBER_IMAGE="ghcr.io/cin/mr-cassop/prober:dev"
# Jolokia is a generic JMX-to-HTTP bridge with no Cassandra-version-specific code,
# so it can stay on the latest published tag rather than a dev build.
export DEFAULT_JOLOKIA_IMAGE="ghcr.io/cin/mr-cassop/jolokia:0.6.6"
export DEFAULT_REAPER_IMAGE="thelastpickle/cassandra-reaper:5.0.1"
export DEFAULT_ICARUS_IMAGE="ghcr.io/cin/mr-cassop/icarus:0.6.5"
# UI has no published release yet (only just added) - build it locally with
# `make docker-build-ui` and it'll pick up this :dev tag. Only used when a
# CassandraCluster opts into spec.ui.enabled.
export DEFAULT_UI_IMAGE="ghcr.io/cin/mr-cassop/ui:dev"

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