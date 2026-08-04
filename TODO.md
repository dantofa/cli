# TODO

## Wave 0: Production-readiness foundation

- [DONE] Cluster update verification: gate PRs on the _upgrade_ path, not just fresh convergence.
- [DONE] Configure cluster backups (Velero cluster-object backup + restore drill). Database backups are out of platform scope: the platform ships no database, so the CloudNativePG operator was dropped (no consumer — Zitadel is not implemented here). A downstream project that owns a database owns its own DB backup capability.
  - [DONE] Velero daily cluster-object backup to DigitalOcean Spaces bucket
- [DONE] Add a restore / DR drill: scheduled verification that both backups actually restore (extend the CI Velero probe from backup-only to backup+restore)
- Deploy Grafana Stack (metrics, logging, alerting) — the observability backend the deferred control-loop / SLO / accuracy-alert items in ROADMAP.md depend on
  - Configure automatic rotation for cluster-local generated secrets (starting with the Grafana admin password): ESO `refreshInterval` to regenerate on a schedule + a Reloader (e.g. Stakater) to restart the consumer so the new value takes effect. Currently generate-once (`refreshInterval: 0`).
- [DONE] Add cost monitoring + budget alerts for DigitalOcean spend (clusters, LoadBalancers, Spaces); flag orphaned/leftover resources
- [DONE] Validate the Let's Encrypt TLS path (`--tls-issuer letsencrypt` + Cloudflare DNS-01) against a real DOKS cluster — built but never run
  - [DONE] Add a staging issuer variant: `--tls-issuer staging` (Let's Encrypt staging ACME server, same Cloudflare DNS-01 solver) so preview and prod select the CA per cluster; production LE stays on prod only, and its rate limits stay off the preview path. Both ACME issuers share one reusable layer, split into two ordered reconcile roots — `cloudflare-api-token` (the Cloudflare DNS-01 token ExternalSecret) then `letsencrypt` (the ClusterIssuer, `dependsOn` the token so it never races an absent secret) — with the issuer name + ACME endpoint substituted per cluster via `${tls_issuer}`/`${acme_server}` cluster-vars.
  - [DONE] Exercise it continuously in preview: `preview.yml` now bootstraps with `--tls-issuer staging` instead of `selfsigned`, so every PR proves the ACME/DNS-01 → Cloudflare flow end-to-end. Staging's high rate limits suit ephemeral per-PR clusters and its untrusted origin certs are fine (Cloudflare Full terminates edge TLS with its own trusted cert).
- [DEFERRED] CI capacity: provision larger / self-hosted runners and re-enable the `local` workflow (currently `workflow_dispatch`-only due to capacity) — also unblocks the restore drill

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
