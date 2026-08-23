---
name: dantofa-platform
description: >-
  Conventions, infrastructure, and reusable primitives of the dantofa platform (the
  base cluster stack every dantofa-provisioned cluster runs). Use when building or
  changing deployment artifacts for a project that runs on a dantofa cluster (local
  kind, DOKS preview/prod): Flux/Kubernetes manifests, Ingress, TLS, secrets, cluster
  provisioning, CI, or observability wiring. Covers domain/ingress/GitOps/secret
  conventions, the local/preview/prod cluster lifecycle, local app access, and the
  required logging/metrics/traces model — so you reuse the platform instead of
  rebuilding what it already provides.
---

# dantofa platform — downstream deployment skill

The dantofa platform (`dctl` + a Flux GitOps tree) provisions Kubernetes clusters and a
fixed base stack. Your downstream project deploys its app **onto** a platform cluster —
so **do not rebuild what the platform already provides**. This skill is the map: match
your task to a section, follow the convention, reuse the primitive.

## What the platform already provides (never duplicate these)
- **Provisioning**: `dctl` creates DOKS + local (kind) clusters, installs Flux, and
  reconciles the shared base stack.
- **Base stacks on every cluster**: cert-manager, External Secrets Operator (ESO),
  Velero (cluster backups), Kyverno (+ baseline governance policies), Trivy (image CVE
  scanning + gate), prometheus-operator CRDs, an always-on local Prometheus (metrics),
  local Loki (logs), local Tempo (traces), Grafana Alloy collectors, and a cost-exporter.
- **Ingress**: Cloudflare Tunnel (DOKS default) or Traefik + external-dns behind a DO
  LoadBalancer (`--dolb`); a local-only Traefik (ClusterIP) on kind.
- **TLS**: cert-manager — selfsigned by default, or Let's Encrypt via Cloudflare DNS-01.
- **Secrets**: Bitwarden via ESO.
- **Observability collection**: Alloy scrapes metrics/logs/traces to the local stores
  and (opt-in) forwards a curated subset to Grafana Cloud.

So: no app-shipped Prometheus/Loki/Tempo/Grafana, no app-shipped ingress controller or
cert-manager, no bespoke log/metric shipping. Emit signals the standard way and the
platform collects them.

## Domain conventions
- Every cluster has a `base_domain` (Flux cluster-var `${base_domain}`) — e.g.
  `local.dantofa.dev`, `preview.dantofa.dev`, `dantofa.dev`.
- **Route apps by PATH on `${base_domain}`, not per-app subdomains**: serve your app at
  `${base_domain}/<app>` (like echo at `/echo`, Grafana at `/grafana`). A per-app
  subdomain (`app.local.dantofa.dev`) sits two levels under the zone apex, outside
  Cloudflare Universal SSL (`*.dantofa.dev`); the apex + one level is covered.
- Never hardcode a hostname — use `${base_domain}` + a path.

## Ingress definitions
Plain `networking.k8s.io/v1` Ingress, **omitting `ingressClassName`** so the cluster's
default IngressClass routes it — one manifest works on every cluster type. Use
`host: ${base_domain}` (Flux substitutes it), a `/<app>` path prefix, and **no `tls:`
block** (the controller serves the default cert). Model on `flux/echo/ingress.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: myapp
  namespace: myapp
spec:
  rules:
    - host: ${base_domain}
      http:
        paths:
          - path: /myapp
            pathType: Prefix
            backend:
              service:
                name: myapp
                port:
                  number: 80
```

## Flux GitOps conventions
- The platform reconciles from ONE source (a `GitRepository` on DOKS, an
  `OCIRepository` on kind), registered at bootstrap. Downstream layers its **own
  payload** as an additional reconcile root pointed at the same source.
- A **reconcile root** is a Flux `Kustomization` CR with `wait: true`, a
  source-agnostic `sourceRef` (`${source_kind}`/`${source_name}`), and
  `postBuild.substituteFrom` the `cluster-vars` ConfigMap. Add `dependsOn` to order
  your root after the platform layers your app needs (e.g. `ingress`, `eso-config`).
- **cluster-vars** (namespace `flux-system`) carries the per-cluster values you
  substitute as `${…}`. Use them; never hardcode:
  `base_domain`, `cluster_name`, `env` (`local`/`preview`/`prod`), `storage_class`,
  `dns_zone`, `tls_issuer`, `acme_server`, `source_kind`/`source_name`, and the
  `node_hourly_price` / `node_transfer_gib` / `block_storage_gib_hourly` /
  `lb_hourly_price` cost constants.
- PVCs must use `storageClassName: ${storage_class}` (→ `do-block-storage` on DOKS,
  `standard` on kind), never a hardcoded class.

## Cluster lifecycle
Use `dctl` directly, or the shared `cluster.just` module — import it (materialized to
`.just/cluster.just` by the devbox plugin, or the flake output `packages.cluster-just`)
and compose YOUR own end-to-end flow over its primitives (the module never calls back
into your recipes):

- **Local (kind)** — `just cluster local create` (create + bootstrap) / `verify` /
  `delete` / `test`. Ingress is a local Traefik ClusterIP; no Cloudflare, no tunnel.
- **Preview (DOKS, ephemeral)** —
  `dctl … cluster bootstrap --base-domain preview.dantofa.dev --tls-issuer staging [--grafana-cloud]`.
  Let's Encrypt **staging** keeps prod rate limits off the preview path; treat as
  disposable.
- **Production (DOKS)** — create with `--ha` and a node size, then
  `dctl … cluster bootstrap --base-domain <domain> --dolb --tls-issuer letsencrypt --env prod [--grafana-cloud]`.
  Traefik + external-dns behind a DO LoadBalancer, a real Let's Encrypt cert.

A downstream e2e is your own recipe: `create → deploy your app → test → delete`.

## Accessing local apps (kind)
kind has no LoadBalancer and the local ingress is a Traefik **ClusterIP**, so reach it
from the host with these `cluster.just` primitives — they port-forward Traefik's
**443** (websecure, self-signed cert) and resolve the real hostname to it, so you use
normal URLs:

- `just cluster local curl https://${base_domain}/myapp` — curl (adds `--connect-to` +
  `--insecure`).
- `just cluster local chromium https://${base_domain}/myapp` — interactive Chromium.
- `just cluster local playwright test` — Playwright. Your `playwright.config` reads the
  exported `HOST_RESOLVER_RULES` into `launchOptions.args` and sets
  `baseURL: https://${base_domain}`; pin `@playwright/test` to the flake's
  `playwright-driver` version. Chromium/Node/Playwright browsers ship in the flake.

Env knobs: `INGRESS_PORT` (default 18443), `CHROMIUM`, `PLAYWRIGHT`.

## Logging
Automatic. Alloy tails every pod's stdout/stderr → local Loki (and Grafana Cloud when
`--grafana-cloud`). **Just log to stdout/stderr** — no per-app log shipping, no
sidecar. Logs are labelled by `cluster`/`env`; query them in Grafana Cloud (Loki).

## Metrics
Cluster/node/pod metrics are collected automatically. Expose **your app's** metrics one
of two ways (both auto-scraped by Alloy — do NOT deploy your own Prometheus):
- A **`ServiceMonitor`/`PodMonitor`** (the prometheus-operator CRDs are installed) — the
  richer path (per-target relabeling, auth, metric filtering); or
- **Annotation autodiscovery** on the pod:
  `k8s.grafana.com/scrape: "true"` + `k8s.grafana.com/metrics.portNumber: "<port>"`
  (path defaults to `/metrics`).

## Traces — REQUIRED
Every app **must** emit **OpenTelemetry traces**. Send **OTLP** to the in-cluster Alloy
receiver (the `applicationObservability` collector); it forwards traces → local Tempo
(and Grafana Cloud), while app OTLP metrics/logs fan out to Prometheus/Loki.
- Point your SDK at the alloy-receiver Service in the `monitoring` namespace — OTLP
  gRPC `:4317` or HTTP `:4318` (confirm the Service name with
  `kubectl -n monitoring get svc`; set `OTEL_EXPORTER_OTLP_ENDPOINT`).
- For LLM/RAG/agent apps, follow the **OpenTelemetry GenAI semantic conventions** (model,
  provider, input/output tokens, etc.) — the platform's per-query cost/latency/accuracy
  analytics are built on those span attributes, so they are not optional.

## Governance (tenant namespaces)
Label your namespace `dantofa.dev/tenant: <app>` to opt into the platform's Kyverno
baseline. It then **enforces** on your Pods: **restricted Pod Security** (run non-root,
drop all capabilities, seccomp `RuntimeDefault`, no host namespaces/paths), **cpu +
memory requests and a memory limit** on every container, and **no `:latest`** image tag
(use a tag or digest). Non-compliant Pods are rejected at admission — design images and
manifests to comply.

## Secrets
Source-of-truth secrets (API tokens, credentials) come from **Bitwarden via ESO**: an
`ExternalSecret` against the `bitwarden` ClusterSecretStore. Disposable, cluster-local
secrets (e.g. a generated password) use the ESO `Password` generator — no Bitwarden
entry. Never commit secrets; never place the DigitalOcean token in a cluster.
