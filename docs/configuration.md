# Configuration

lok8s supports configuration via a YAML file. By default, it looks for `~/.lok8s.yaml`. Use `--config /path/to/config.yaml` to point at a different file.

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

Project-specific configuration (the environment, node counts, allocated subnets, MetalLB IP ranges, etc.) is persisted separately under `~/.lok8s/<project>.yaml` after a successful `create`, and is managed with the `lok8s config` subcommand (`list`, `show`, `delete`) - see the main [README](../README.md#usage) for usage examples.
