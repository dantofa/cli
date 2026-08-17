# Reference dashboards

Self-contained, **Git-Sync-ready** dashboard sources. The platform does **not**
deploy these — visualization is managed centrally (a separate repo via Grafana Cloud
[Git Sync](https://grafana.com/docs/grafana-cloud/as-code/observability-as-code/git-sync/)
or the Terraform `grafana` provider). This tree is the reference implementation of
the dashboards our emitted metrics are designed to feed; lift it into the central
repo, or point Git Sync's *Repository folder* here.

Each dashboard is raw exported dashboard JSON (one per file, stable `uid`, title in
the JSON), datasource-agnostic via a `$datasource` template variable, so it imports
unchanged into any Grafana with a Prometheus datasource. Subdirectories map to Grafana
folders; `.folder.json` pins each folder's `uid`/`title`.

## `platform/cluster-cost.json` — per-cluster cost (estimated)

A list-price estimate of each cluster's monthly DO spend for comparing clusters — not
the DO invoice (account-level spend stays with DO billing). It multiplies
always-collected metrics by the price series the **cost-exporter** emits
(`flux/cluster/cost-exporter/`), joined on `cluster`.

### Metric contract

The dashboard depends on these series; treat the names/labels as a stable interface.

Price series (from the cost-exporter; carry `cluster`/`env` labels via Alloy):

| Series | Meaning |
| --- | --- |
| `dantofa_node_hourly_price_usd` | hourly USD price of one worker node |
| `dantofa_node_transfer_gib` | included monthly outbound transfer per node, GiB |
| `dantofa_block_storage_gib_hourly_usd` | USD per GiB-hour of block storage |
| `dantofa_lb_hourly_price_usd` | USD per hour per LoadBalancer Service |

Base series (kube-state-metrics + node-exporter, already collected): `kube_node_info`,
`kube_persistentvolume_capacity_bytes`, `kube_service_spec_type`,
`node_network_transmit_bytes_total`.

### Assumptions
- Single node pool per cluster (platform invariant), so one node price is exact.
- `device="eth0"` is the public-egress proxy; traffic is an **upper-bound estimate**
  (DO bandwidth is a pooled per-node allowance billed at $0.01/GiB over it).
- Monthly figures use DO's 730-hour convention.
