# Contributing Guidelines

Welcome to mr-cassop! We appreciate your interest in contributing to this Kubernetes operator for managing Apache Cassandra clusters.

When contributing to this repository, please first discuss the change you wish to make via an issue. 
This way we have traceability (including `Resolves`) and can discuss the issue in a documented, open fashion.

## Development Setup

### Prerequisites

- Kubernetes 1.28+ (minikube, kind, or colima for local development)
- Go 1.24+ with enabled go modules
- Node.js 20+ for documentation changes
- Docker with buildx support
- Helm 3.15+
- kubectl configured for your cluster

### Quick Start

1. **Clone and setup**
   ```bash
   git clone https://github.com/your-org/mr-cassop.git
   cd mr-cassop
   ```

2. **Follow the development guide**
   
   For detailed setup instructions, build options, and testing procedures, see the [Development Guide](docs/docs/development.md).

3. **One-line setup** (for the impatient)
   ```bash
   ./build-local.sh --cassandra && make install && kubectl create namespace mr-cassop-system && helm install mr-cassop ./mr-cassop -n mr-cassop-system -f local-values.yaml
   ```

## Pull Requests Welcome

* Add clear PR name, it'll go in the release notes.
* Set label for PR:
- Breaking Changes -> breaking-change
- New Features -> enhancement
- Bug Fixes -> bug
- Improvements -> *
* Add `Resolves #issue_number`.
* Document release steps if the upgrade requires user interaction.

Every PR MUST be reviewed by at least two maintainers before it can get merged.

The maintainers will review your PR and notify you if any information is missing.

## Reporting issues

Please open an issue if you would like to discuss anything that could be improved or have suggestions.
