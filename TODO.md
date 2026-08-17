# TODO

## Wave 0: Production-readiness foundation

- [DONE] Cluster update verification: gate PRs on the _upgrade_ path, not just fresh convergence.
- [DONE] Configure cluster backups (Velero cluster-object backup + restore drill). Database backups are out of platform scope: the platform ships no database, so the CloudNativePG operator was dropped (no consumer — Zitadel is not implemented here). A downstream project that owns a database owns its own DB backup capability.
  - [DONE] Velero daily cluster-object backup to DigitalOcean Spaces bucket
- [DONE] Add a restore / DR drill: scheduled verification that both backups actually restore (extend the CI Velero probe from backup-only to backup+restore)
- [IN PROGRESS] Deploy the observability stack — the backend the deferred control-loop / SLO / accuracy-alert items in ROADMAP.md depend on. Refactored from the initial all-in-one `kube-prometheus-stack` into a layered model: **unconditional collection + local retention (base)**, **opt-in visualization server (in-cluster Grafana and/or Grafana Cloud)**. Collection is Alloy-centric (scrape once, `remote_write` to a destinations list: local Prometheus always, remote opt-in with allowlist + `cluster`/`env` labels). Grafana is operator-managed so dashboards/datasources/alerts are CRDs that reconcile into an in-cluster **or** external (Grafana Cloud) instance via `instanceSelector`. Single cloud stack, clusters distinguished by labels; cost controlled by egress allowlist (a naive ship-everything setup was ~4× cluster cost).
  - [DONE] Move monitoring agents from `prometheus-operator-crds` to `k8s-monitoring`
  - [DONE] Add Grafana Cloud support as an opt-in remote destination
  - [DONE] Add a per-cluster cost dashboard in Grafana Cloud
  - Add log collector to base monitoring agents
  - Add Otel collector to base monitoring agents
- [DONE] Add cost monitoring + budget alerts for DigitalOcean spend (clusters, LoadBalancers, Spaces); flag orphaned/leftover resources
- [DONE] Validate the Let's Encrypt TLS path (`--tls-issuer letsencrypt` + Cloudflare DNS-01) against a real DOKS cluster — built but never run
  - [DONE] Add a staging issuer variant: `--tls-issuer staging` (Let's Encrypt staging ACME server, same Cloudflare DNS-01 solver) so preview and prod select the CA per cluster; production LE stays on prod only, and its rate limits stay off the preview path. Both ACME issuers share one reusable layer, split into two ordered reconcile roots — `cloudflare-api-token` (the Cloudflare DNS-01 token ExternalSecret) then `letsencrypt` (the ClusterIssuer, `dependsOn` the token so it never races an absent secret) — with the issuer name + ACME endpoint substituted per cluster via `${tls_issuer}`/`${acme_server}` cluster-vars.

## Downstream projects

- Add Saas repository with initial agent framework. Interesting features:
  -- Dev+Ops of RAG apps (ISO chatbot)
  -- Dev+Ops of Cloudflare workers/pages web apps
  -- Dev+Ops of Android/iPhone applications
  -- Dev+Ops of Web scraping/analytics apps for used car markets in CR and VE
  -- Dev+Ops of Web scraping/analytics apps for real state market in CR and VE
  -- Dev+Ops of Web scraping/analytics apps for TikTok sentiment analytics in CR and VE
  -- Dev+Ops of Elixir game server backend
- Add CLI/Operator repository for git repository management

## Platform

- [DONE] Add Trivy deployment manifests
- [DONE] Create reusable justfile template for common operations in downstream projects (e.g. local/preview/prod clusters)
- [DONE] Add justfile plugin configuration for downstream project management
- [DONE] Add image security gate
- [DEFERRED] Add support for GKE/EKS clusters
