# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Dartboard is a tool to run scalability and performance tests on the Rancher product family. It supports deploying infrastructure on AWS, Azure, Harvester, or local k3d; configuring Rancher; and executing load tests via k6.

## Building and Development

### Build Commands

```bash
# Build the dartboard binary (downloads vendored binaries first)
make build

# Clean build artifacts
make clean
```

The build process:
- Downloads and vendors binaries: OpenTofu 1.8.2, kubectl 1.31.1, Helm 3.16.1, k3d 5.7.4
- Extracts vendored binaries to `internal/vendored/bin/` at build time
- Decompresses and stores them in `.bin/` directory at runtime
- Uses `CGO_ENABLED=0` for static binary compilation
- Requires Go 1.24.2

### Running Dartboard

```bash
# Full deployment with Rancher, infrastructure, and load tests
dartboard deploy --dart=./darts/my_dart.yaml

# Just deploy infrastructure via OpenTofu (no Rancher/charts)
dartboard apply --dart=./darts/my_dart.yaml

# Just run k6 load tests (assumes Rancher already deployed)
dartboard load --dart=./darts/my_dart.yaml

# Get cluster access information
dartboard get-access --dart=./darts/my_dart.yaml

# Tear down all infrastructure
dartboard destroy --dart=./darts/my_dart.yaml

# Destroy and recreate infrastructure only (no Rancher)
dartboard reapply --dart=./darts/my_dart.yaml

# Destroy and redeploy everything
dartboard redeploy --dart=./darts/my_dart.yaml
```

Deploy command flags:
- `--skip-apply`: Skip OpenTofu apply phase, assume infrastructure exists
- `--skip-charts`: Skip Helm chart installation
- `--skip-refresh`: Skip OpenTofu refresh phase

## Architecture

### High-Level Flow

1. **OpenTofu modules** deploy infrastructure and Kubernetes clusters
2. **dartboard** Go application:
   - Runs OpenTofu to create clusters
   - Uses Helm/kubectl to deploy Rancher and test software
   - Orchestrates load testing via k6
3. **k6 scripts** benchmark APIs and send metrics to Mimir
4. **Dart YAML files** define complete test environments

### Cluster Types

- **upstream**: Where Rancher is installed
- **downstream**: Clusters imported into Rancher (zero or more)
- **tester**: Where load testing/benchmarking/metric collection tools run

### Project Structure

```
cmd/dartboard/           - Main CLI application and subcommands
internal/
  actions/               - High-level orchestration (cluster provisioning, Rancher setup)
  dart/                  - Dart YAML file parsing and configuration
  tofu/                  - OpenTofu wrapper and state management
  helm/                  - Helm wrapper
  kubectl/               - kubectl wrapper
  vendored/              - Vendored binary management
  harvester/             - Harvester-specific utilities
  docker/                - Docker client wrapper
  k3d/                   - k3d cluster management
tofu/
  main/                  - Platform-specific main modules (aws, azure, harvester, k3d)
  modules/
    generic/             - Platform-agnostic modules (k3s, rke2, etcd, test_environment)
    aws/                 - AWS-specific modules (node, network, rds)
    azure/               - Azure-specific modules (aks, node, network)
    k3d/                 - k3d-specific modules
k6/                      - Load test JavaScript files for k6
darts/                   - Example Dart YAML configuration files
```

### OpenTofu Module Naming Conventions

Modules follow consistent naming based on the concept they represent:

- **node**: A Linux VM capable of SSH login
  - `node_variables`: Block of variables for VM creation, specific to one VM
- **cluster**: A Kubernetes cluster (nodes with distribution, or managed service)
- **network**: Shared networking infrastructure (networks, firewalls, bastion hosts)
  - `network_configuration`: Block of outputs from network module to node modules
- **test_environment**: Upstream cluster + downstream clusters + tester cluster + network

## Dart Files

Dart YAML files in `darts/` represent full test environments:

```yaml
tofu_main_directory: ./tofu/main/k3d  # Points to platform-specific main module
tofu_parallelism: 10
tofu_variables:                        # Passed directly to OpenTofu
  project_name: st
  upstream_cluster: {...}
  downstream_cluster_templates: [...]
  tester_cluster: {...}

chart_variables:                       # Helm chart configuration
  rancher_version: 2.9.1
  admin_password: adminadminadmin
  rancher_replicas: 1
  # Override image: rancher_image_override, rancher_image_tag_override
  # Custom values: rancher_values: |

test_variables:                        # Load test parameters
  test_config_maps: 2000
  test_secrets: 2000
```

Examples in `darts/`:
- `k3d.yaml`: Local k3d deployment (default)
- `aws.yaml`, `azure.yaml`: Cloud deployments
- `harvester.yaml`: Harvester deployment
- `prairie-*.yaml`: Large-scale test configurations

## Key Dependencies

- **Shepherd** (`github.com/rancher/shepherd`): Library for Rancher API interactions
  - Currently using local replace: `../shepherd`
  - Provides clients for Rancher management API
  - Extensions for clusters, tokens, kubeconfig, etc.
- **Rancher APIs** (`github.com/rancher/rancher/pkg/apis`): Kubernetes CRD types
- **Kubernetes client-go**: v0.32.2 (pinned across all k8s.io modules)

## Development Conventions

### Code Organization

- Use `internal/actions` for high-level orchestration logic
- Use `internal/<tool>` packages for wrapping external tools (tofu, helm, kubectl)
- Keep platform-specific logic in OpenTofu modules, not Go code

### Workarounds and Hacks

Document workarounds with `HACK:` comments (searchable in PRs):
```go
// HACK: Working around bug in dependency X version Y
// Remove when dependency is updated to version Z
```

### Shepherd Usage

When interacting with Rancher:
- Create a Rancher client via `SetupRancherClient()` in `internal/actions/rancher.go`
- Use Shepherd extensions for common operations (cluster provisioning, token management)
- Follow patterns in `internal/actions/clusters.go` for cluster operations

## Common Workflows

### Testing Custom Rancher Images on k3d

Set in dart file:
```yaml
chart_variables:
  rancher_image_override: rancher/rancher
  rancher_image_tag_override: v2.8.6-debug-1
```

When using k3d, if an image with the same tag exists locally, it will be added to clusters.

### Using Remote Docker Host

```bash
export DOCKER_HOST=tcp://remotehost:2375
```

### Debugging OpenTofu

```bash
export TF_LOG=debug
dartboard apply --dart=./darts/my_dart.yaml
```

### SSH Tunnel Management

Tunnels are created automatically for some platforms (e.g., AWS). If broken:
```bash
./config/open-tunnels-to-upstream-*.sh
```

Force stop all SSH tunnels:
```bash
pkill -f 'ssh .*-o IgnoreUnknown=TofuCreatedThisTunnel.*'
```

## Testing and Load Generation

k6 scripts in `k6/` directory perform various operations:
- `k8s_api_benchmark.js`: Kubernetes API benchmarking
- `api_benchmark.js`: Rancher API benchmarking
- `create_k8s_resources.js`: Create ConfigMaps, Secrets
- `create_projects.js`, `create_roles_users.js`: Rancher resource creation
- `steve_*.js`: Steve API (Rancher's k8s proxy) testing

Scripts run from pods in the tester cluster and send metrics to Mimir for analysis.

## Important Notes

- OpenTofu state is workspace-specific and stored per dart configuration
- Clusters should be reachable either directly or via SSH bastion from the machine running OpenTofu
- Deployed nodes can reach each other with same domain names from their shared network
- The `load` command assumes infrastructure and Rancher are already deployed
- Custom cluster provisioning uses the Shepherd library for Rancher integration
