# Provisioning-engine evaluation

**Status:** Decided — stay bespoke, do not migrate.
**Date:** 2026-08-19.
**Question:** Is `dctl` rebuilding a custom Cluster API + Sveltos, and should the project
migrate to any of CAPI, Sveltos, Crossplane, or Flux's tofu-controller?

## TL;DR

**No migration is warranted.** Migration is the wrong verb: no alternative *subsumes*
what the project is. Each could replace at most one layer's engine — CAPI / Crossplane /
tofu-controller the provisioning layer, Sveltos the add-on-delivery layer — while the
~70% of `internal/core` that is the actual product (cost model, Spaces/Velero credential
linking, Bitwarden/ESO wiring, the curated toolset, graceful DNS teardown) has no analog
in any of them and stays ours in every scenario. The one layer any of them would replace
works fine today, and the framework-free `core` + adapter seams already let us adopt any
of them *later, as an adapter*, without a migration. So the option value is already paid
for; adopting early only adds cost.

## What the project is (and why it isn't CAPI+Sveltos)

`dctl` is an opinionated, one-shot CLI that provisions a DOKS/kind cluster and bootstraps
a curated Flux platform onto it. It is single-cluster, imperative, and leans on DOKS's
managed control plane (which is why the opinionated builders bake in the DO autoscaler,
auto-upgrade, and surge-upgrade rather than managing machines themselves).

CAPI and Sveltos are *declarative control-plane / fleet* systems: controllers running in a
management cluster that continuously reconcile cluster lifecycle (CAPI) or fan add-ons out
to many label-selected clusters (Sveltos). The overlap with `dctl` is **conceptual, not
architectural** — there is no control plane, no CRDs of our own, no fleet, no reconciliation
loop. The closest honest analogy today is a `clusterctl`-lite plus a bespoke curated Flux
distribution.

## The cost axis (why DOKS was chosen, and what actually drives it)

DOKS was selected on cost. The comparison across DOKS / GKE / AKS / EKS / Civo / Linode /
Vultr / Scaleway / OCI showed the cost advantage is **not** in node compute — at a matched
4 vCPU / 16 GB dedicated node every provider lands in a ~$120–145/mo on-demand band. The
structural gaps are three line items:

| Line item     | Boutique (DO/Civo/Linode/Vultr/Scaleway) | Hyperscaler (EKS/GKE/AKS)        |
| ------------- | ---------------------------------------- | -------------------------------- |
| Control plane | $0 (DO HA +$40)                          | $73/mo **per cluster** (EKS ext. $438) |
| Egress        | free / bundled / $0.01–0.005/GB          | $0.087–0.12/GB metered           |
| Load balancer | $10–12                                   | $18–50                           |
| Small cluster | ~$130–156                                | ~$318–391                        |

The control-plane fee is per-cluster, so it multiplies with a fleet; egress multiplies with
traffic and is excluded from every node-only comparison. This is the mechanism behind the
key finding below.

## Cost and CAPI-managed availability pull in opposite directions

CAPI needs a per-cloud infrastructure provider, and providers come in two kinds:

- **Managed-control-plane provider** — drives the cloud's *managed* K8s API (you keep the
  free/managed control plane). Exists for EKS (CAPA), GKE (CAPG), AKS (CAPZ), OKE (CAPOCI).
- **Self-managed provider** — provisions raw VMs and runs a control plane *you* now own and
  operate. This is all that exists for DigitalOcean (CAPDO — Droplets, **not** DOKS),
  Linode (CAPL), Vultr (CAPV), Scaleway (CAPS), Hetzner (CAPH).

So **adopting CAPI on a boutique cloud abandons the managed control plane** — the exact
thing that made the cloud cheap — and takes on control-plane ops (etcd backups, patching,
HA, upgrades). The cheap clouds have no managed-K8s CAPI provider; the managed-CAPI clouds
are the expensive ones. **Oracle OKE is the lone cheap + CAPI-managed cell** (free control
plane, 10 TB/mo free egress, ARM free tier, CAPOCI drives managed OKE) — worth a spike if a
CAPI-native path is ever wanted without hyperscaler pricing.

## Crossplane / tofu-controller: managed provisioning without CAPI

Unlike CAPI, two in-cluster CRD mechanisms **can** provision the boutique clouds' *managed*
control planes, keeping the cost advantage:

- **Crossplane provider** (typed CRD per resource): DigitalOcean
  (`crossplane-contrib/provider-digitalocean`, community, DO-blessed), Scaleway and Linode
  (first-party), Civo (`upsidr/provider-civo-upjet`, community). A Crossplane Composition
  could express our opinionated invariants as a typed `PlatformCluster` abstraction — the
  YAML analog of today's Go builders.
- **Flux tofu-controller** (`flux-iac/tofu-controller`): a `Terraform` CRD that runs any
  Terraform/OpenTofu module in-cluster with drift detection. Works for all five clouds
  (including Vultr) via each cloud's Terraform provider, and slots into our existing
  Flux-native GitOps model with the least friction.

Both **strictly dominate CAPI on our clouds**, but both add a persistent management-cluster
dependency and a new in-cluster state source to back up.

## Verdict and reasoning

| Alternative     | Migrate now? | Why                                                                 |
| --------------- | ------------ | ------------------------------------------------------------------- |
| CAPI            | No           | Doesn't apply to DOKS (self-managed only); adopting = abandoning managed K8s. |
| Sveltos         | No           | Flux already covers add-ons at single-cluster; adds a management cluster for zero current benefit. |
| Crossplane      | No           | Real fit, but pays off only at fleet scale + declarative drift correction we don't need (DOKS already auto-manages nodes). |
| tofu-controller | No           | Lowest-friction, but trades a working `godo` call for a controller + in-cluster Terraform state; no current win. |

Every alternative solves a problem the project does not yet have — a *persistent management
cluster* driving a *fleet* with *centralized, declarative, drift-corrected* provisioning.
The project is single-cluster, one-shot, DOKS-managed. At that shape all of them **increase**
complexity (a management-cluster operational tax, new state to back up), none reduce it, and
none touch the 70% that is the product. Building for an unconfirmed multi-cloud/BYOC future
is premature abstraction; the framework-free `core` already bounds the later migration cost.

## Adoption triggers (the decision rule)

Watch for these rather than re-litigating:

- **Crossplane / tofu-controller** — provisioning managed clusters on **≥2 clouds** *and*
  wanting one declarative, drift-corrected control point *and* willing to run a management
  cluster.
- **Sveltos** — a management cluster fanning add-ons to **N workload clusters with divergent
  per-cluster config**.
- **CAPI** — leaving managed K8s for **self-managed control planes**.
- **BYOC at fleet scale, centralized** — the combined trigger (Crossplane provisioning +
  Sveltos add-ons).

## Recommended hedge (not a migration)

Lift the DOKS cluster logic behind a provider-neutral `Provisioner` interface in
`internal/core/cluster`, and split **provision** from **adopt + install-platform** (the
bring-your-own-cluster seam). This is not adopting any alternative; it makes a future swap
to one a single-adapter change instead of a rewrite, and de-risks the multi-cloud and BYOC
directions at once. Graceful DNS teardown (`core/teardown`) stays bespoke regardless.
