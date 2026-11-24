# Changelog

## v0.2.0 - 2026-11-24
### What's Changed
#### 🚀 Features
* The Kubernetes collector reports node versions: `kubelet` (named `k3s`, `rke2` or `kubernetes` after the build suffix) and `kernel`, one item per distinct version. Nodes are optional — a ClusterRole without access to them still yields charts and images, and the skip is logged.
* `deploy/kubernetes.yaml`: ServiceAccount, ClusterRole, ClusterRoleBinding, ConfigMap, Deployment and Service in one file, so the service starts with a single `kubectl apply`. A test keeps its image tag and its ClusterRole in step with the release.
#### ⚠️ Upgrade note
* Add `nodes` (`get`, `list`) to the ClusterRole to get node versions. Without it nothing breaks; the items are simply absent.

## v0.1.0 - 2026-11-24
### What's Changed
#### 🚀 Features
* First release. Hosts push their versions to `POST /v1/report`, the Kubernetes collector reads k3s `HelmChart` resources and the images of live pods, and `GET /v1/inventory` returns everything with each item mapped to its upstream repository.
* Two listeners: the public one accepts reports only, the internal one serves the inventory. A per-source bearer token identifies the reporter; the config stores only token hashes.
* Drift is part of the answer: items without a mapping come back as `unmapped`, and a configured source that stopped reporting comes back as `stale`.
* `agent/inventory-agent.sh`: a host agent that needs only `sh` and `curl`.
