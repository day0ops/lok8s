# lok8s

lok8s (short for "Local Kubernetes") is a tool for provisioning local Kubernetes clusters with advanced network management capabilities.

![demo](images/demo.png)

## Why does this tool exist ?

One of the painful gaps in managing local Kubernetes clusters is in the area of multi-cluster setups. Developers working on microservices architectures, multi-tenant applications, or testing distributed systems often need multiple isolated Kubernetes clusters that can communicate with each other. However, most local Kubernetes tools (Kind, Minikube, etc.) create clusters in isolated network environments, making it difficult or impossible to:

- Establish network connectivity between clusters for inter-cluster communication
- Test service mesh deployments that span multiple clusters
- Simulate real-world multi-cluster scenarios where services need to communicate across cluster boundaries

Often these clusters are hosted in private segregated networks without the ability to manage traffic between them. This limitation forces developers to either deploy to cloud environments (adding cost and complexity) or work around network isolation issues manually, which is time-consuming and error-prone.

lok8s addresses this gap by providing automatic network management that creates isolated but routable network segments for each cluster, enabling true multi-cluster development and testing scenarios on local machines.

Features:

- **Multiple Cluster Providers**: 
  - Kind clusters with Docker/Podman support
  - Minikube clusters with KVM2 (Linux) and VFKit (macOS) drivers
- **Advanced Networking**: 
  - Integrated libvirt network management (Linux)
  - Automatic network isolation between clusters
  - Custom subnet configuration
- **Load Balancer Support**: Automatic MetalLB installation and configuration
- **CNI**: Cilium as the default and preferred CNI
- **Multi-Cluster Management**: Create and manage up to 3 clusters per project
- **Registry Caching**: Built-in Docker registry mirror support for faster image pulls
- **Cloud-like Topology**: Clusters are configured with region/zone labels

For now this tool is limited to macOS and Linux platforms.

## Installation

### Prerequisites

#### Common Requirements
- Go 1.24 or later
- Docker or Podman

#### Linux-Specific Requirements
- libvirt and libvirtd
- qemu-kvm
- User must be in the `libvirt` group

#### macOS-Specific Requirements
- VFKit (for Minikube multi-cluster setups)
- vmnet-helper (for advanced networking)

### Download the binary

```bash
curl -fsSL https://raw.githubusercontent.com/day0ops/lok8s/refs/heads/main/install.sh | bash
```

### Building from Source

```bash
git clone <repository-url>
cd lok8s
make build
make install
```

## Usage

### Creating Clusters

Create clusters (defaults to Minikube, use `--environment kind` for Kind):
```bash
# Create a single Minikube cluster (default)
lok8s create -p myproject -n 1

# Create multiple Minikube clusters with custom resources
lok8s create -p myproject -n 2 \
  --cpu 4 \
  --memory 8GiB \
  --bridge virbr50 \
  --subnet-cidr 10.89.0.1/16 \
  --nodes 3

# Create Kind clusters
lok8s create -p myproject -n 1 --environment kind

# Create multiple Kind clusters with custom networking
lok8s create -p myproject -n 3 \
  --environment kind \
  --gateway-ip 10.89.0.1 \
  --subnet-cidr 10.89.0.0/16 \
  --nodes 2 \
  --kubernetes-version 1.31

# Create without MetalLB
lok8s create -p myproject -n 1 --environment kind --skip-metallb-install
```

### Deleting Clusters

Delete clusters:
```bash
# Delete Minikube clusters (default)
lok8s delete -p myproject -n 2

# Delete Kind clusters
lok8s delete -p myproject -n 3 --environment kind

# Force delete (removes networks and config files)
lok8s delete -p myproject -n 2 --force
```

### Managing Kind Tunnels

The `kind-tunnel` command starts cloud-provider-kind background processes that enable LoadBalancer services in Kind clusters.

```bash
# Start cloud-provider-kind processes (run the tunnel)
# macOS: requires sudo
sudo lok8s kind-tunnel -p myproject
# Linux: no sudo required
lok8s kind-tunnel -p myproject

# Terminate cloud-provider-kind processes (tear down the tunnel)
# macOS: requires sudo
sudo lok8s kind-tunnel -p myproject --terminate
# Linux: no sudo required
lok8s kind-tunnel -p myproject --terminate

# Show ephemeral ports created by Docker/Podman for load balancers
# macOS: requires sudo
sudo lok8s kind-tunnel -p myproject --ports
# Linux: no sudo required
lok8s kind-tunnel -p myproject --ports

# Show ports in JSON format
sudo lok8s kind-tunnel -p myproject --ports --format json
lok8s kind-tunnel -p myproject --ports --format json
```

**Note:** On macOS, sudo is required to access Docker privileged ports. On Linux, sudo is not required.

### Global Options

```bash
# Enable verbose logging
lok8s --verbose kind create -p myproject -n 1

# Use custom config file
lok8s --config /path/to/config.yaml kind create -p myproject -n 1
```

## Configuration

The tool supports configuration via YAML file. By default, it looks for `~/.lok8s.yaml`:

```yaml
# Default configuration values
kind:
  network_name: "kind"
  gateway_ip: "10.89.0.1"
  subnet_cidr: "10.89.0.0/16"
  node_count: 1

minikube:
  cpu: "4"
  memory: "8GiB"
  disk_size: "10GiB"
  node_count: 2
  bridge: "virbr50"
  subnet_cidr: "10.89.0.1/16"

metallb:
  version: "0.14.9"
  range_min_octet: "200"
  range_max_octet: "254"

network:
  default_bridge: "virbr50"
  qemu_uri: "qemu:///system"
```

## Code Structure

The tool is structured as follows:

```
cmd/
└── main.go

pkg/
├── cmd/
│   ├── root.go
│   └── kind_tunnel.go
├── cluster/
│   ├── kind/
│   └── minikube/
│       ├── manager.go
│       └── binary_manager.go
├── config/
│   ├── config.go
│   └── project_config.go
├── logger/
│   ├── logger.go
│   ├── formatter.go
│   ├── spinner.go
│   ├── status.go
│   └── terminal.go
├── network/
│   ├── network.go
│   ├── network_linux.go
│   ├── network_darwin.go
│   └── subnet.go
├── services/
│   ├── metallb.go
│   ├── cilium.go
│   └── cloud_provider_kind.go
└── util/
    ├── docker/
    ├── github/
    ├── helm/
    ├── k8s/
    ├── version/
    └── retry.go
```

## Supported Kubernetes Versions

### Kind
- 1.34.x
- 1.33.x
- 1.32.x  
- 1.31.x
- 1.30.x
- 1.29.x

### Minikube
- 1.34.x
- 1.33.x
- 1.32.x
- Any valid semantic version (will be validated)

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Run `make test` and `make lint`
6. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
