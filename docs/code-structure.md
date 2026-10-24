# Code Structure

```
cmd/
└── main.go

pkg/
├── cmd/
│   ├── root.go
│   └── kind_tunnel.go
├── cluster/
│   ├── kind/
│   │   └── manager.go
│   ├── minikube/
│   │   ├── manager.go
│   │   └── binary_manager.go
│   └── native/
│       ├── manager.go
│       ├── client.go             # NodeClient interface + backend-selecting factory
│       ├── container_client.go   # macOS backend (Apple `container` CLI)
│       ├── incus_client.go       # Linux backend (Incus)
│       ├── lima_client.go        # cross-platform backend (Lima, via limactl)
│       ├── k3s.go
│       ├── subnet.go
│       └── kubeconfig_merge.go
├── config/
│   ├── config.go
│   ├── project_config.go
│   └── version.go
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
    ├── exec.go
    └── retry.go
```

Each cluster provider (`kind`, `minikube`, `native`) implements the same shape of `Manager`: `CreateClusters`, `DeleteClusters`, `StatusClusters`, `ListClusterNames`, `LoadImage`. The native provider itself has three selectable VM backends (`container`, `incus`, `lima`), chosen via `--native-vm-backend`. `pkg/services` (Cilium, MetalLB, cloud-provider-kind) and `pkg/util` (Helm, Kubernetes clients, Docker/Podman, GitHub releases) are shared across all three providers.
