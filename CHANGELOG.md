# Changelog

## v0.1.0 - 2026-11-24
### What's Changed
#### 🚀 Features
* First release. Hosts push their versions to `POST /v1/report`, the Kubernetes collector reads k3s `HelmChart` resources and the images of live pods, and `GET /v1/inventory` returns everything with each item mapped to its upstream repository.
* Two listeners: the public one accepts reports only, the internal one serves the inventory. A per-source bearer token identifies the reporter; the config stores only token hashes.
* Drift is part of the answer: items without a mapping come back as `unmapped`, and a configured source that stopped reporting comes back as `stale`.
* `agent/inventory-agent.sh`: a host agent that needs only `sh` and `curl`.
