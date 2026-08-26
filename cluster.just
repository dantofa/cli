# cluster.just -- reusable cluster-ops recipes for the dantofa platform and its
# downstream projects, under one namespace: `just cluster <area> <action>`.
#
#   just cluster debug                              # snapshot cluster + Flux state
#   just cluster verify backup|restore|image-scan   # verify platform infra
#   just cluster local  create|verify|delete|test   # kind cluster lifecycle
#   just cluster local  ca                          # export the local TLS trust anchor
#
# Distributed via the platform Nix flake (`packages.cluster-just`): the platform
# imports it directly; a downstream repo materializes it (flake output / the devbox
# plugin, which also ensures the justfile imports it). These are
# PRIMITIVES -- compose your own e2e (create -> deploy+test your app -> delete) in
# your OWN recipe; the module deliberately does not call back into project recipes.
#
# Config is read from the environment with sane defaults (set in your dev shell /
# devbox / direnv; no just-variable overrides needed):
#   DCTL              how to invoke dctl (default: `nix run github:dantofa/platform#default --`)
#   BASE_DOMAIN       ingress FQDN for `cluster local` (default: local.dantofa.dev)
#   KUBECONFIG        kubeconfig path (default: ./.kubeconfig)
#   LOCAL_CA_FILE     where `cluster local ca` writes the trust anchor (default: ./.local.pem)
#   TRIVYIGNORE_BASE  mandatory accepted-CVE file (default: ./.trivyignore-cluster)
#   TRIVYIGNORE_LOCAL optional extra accepted-CVE file (default: ./.trivyignore-cluster)

# Dispatchers: `just cluster <area> <action>` -> `just cluster-<area>-<action>`,
# extending the platform's existing `local {{action}}` / `github {{action}}` pattern.
cluster area *args:
  just cluster-{{area}} {{args}}

cluster-verify action *args:
  just cluster-verify-{{action}} {{args}}

cluster-local action *args:
  just cluster-local-{{action}} {{args}}

# Integration check (CI): assert Flux installed Velero and a backup completes.
# Targets the cluster in $KUBECONFIG; run after bootstrapping the backup stack.
cluster-verify-backup:
  #!/usr/bin/env bash
  set -euo pipefail
  ns=velero
  echo "Waiting for Flux to install the Velero HelmRelease..."
  for _ in $(seq 1 90); do
    if kubectl -n "$ns" get helmrelease velero >/dev/null 2>&1; then break; fi
    sleep 10
  done
  kubectl -n "$ns" wait --for=condition=Ready --timeout=600s helmrelease/velero
  echo "Waiting for the BackupStorageLocation to become Available..."
  kubectl -n "$ns" wait --for=jsonpath='{.status.phase}'=Available \
    backupstoragelocation/default --timeout=300s
  echo "Issuing a test backup..."
  kubectl -n default create configmap velero-ci-probe \
    --from-literal=ok=1 --dry-run=client -o yaml | kubectl apply -f -
  backup="ci-verify-$(date +%s)"
  velero backup create "$backup" --namespace "$ns" --include-namespaces default --wait || true
  # Fully-qualified resource.group (backups.velero.io) so the short `backup` name
  # can never resolve to a different group's Backup CRD if one is later installed.
  phase="$(kubectl -n "$ns" get backups.velero.io "$backup" -o jsonpath='{.status.phase}')"
  echo "Backup $backup phase: $phase"
  if [ "$phase" != "Completed" ]; then
    velero backup describe "$backup" --namespace "$ns" --details || true
    # `velero backup logs` streams from object storage, which a CI runner can't
    # reach; the velero server pod logs carry the same backup errors and are
    # always reachable, so dump them for the actual failure reason.
    kubectl -n "$ns" logs deploy/velero --tail=200 || true
    velero backup logs "$backup" --namespace "$ns" || true
    # Capacity signals: node pressure, pod restarts/placement, last-terminated
    # reasons (OOMKilled), and recent Warning events (evictions, scheduling).
    echo "--- nodes ---"; kubectl get nodes -o wide || true
    echo "--- $ns pods ---"; kubectl -n "$ns" get pods -o wide || true
    echo "--- last terminated reasons ($ns) ---"
    kubectl -n "$ns" get pods -o jsonpath='{range .items[*]}{.metadata.name}{" -> "}{.status.containerStatuses[*].lastState.terminated.reason}{"\n"}{end}' || true
    echo "--- recent Warning events ---"
    kubectl get events -A --field-selector type=Warning --sort-by=.lastTimestamp | tail -30 || true
    exit 1
  fi

# Backup + restore drill: prove a backup can actually be RESTORED, not just taken
# (an untested backup is not a backup). Seeds a throwaway namespace, backs it up,
# deletes it to simulate loss, restores from the backup, and asserts the seeded
# marker returns. Run after `cluster verify backup` (which waits for Velero + an
# Available BackupStorageLocation). Fully-qualified resource.group on
# `backups`/`restores` (backups.velero.io / restores.velero.io) so the short names
# can never resolve to a different group's CRD if one is later installed.
cluster-verify-restore:
  #!/usr/bin/env bash
  set -euo pipefail
  ns=velero
  drill=velero-restore-drill
  kubectl -n "$ns" wait --for=jsonpath='{.status.phase}'=Available \
    backupstoragelocation/default --timeout=300s
  # Seed a throwaway namespace with a marker to back up and later assert on.
  kubectl create namespace "$drill" --dry-run=client -o yaml | kubectl apply -f -
  canary="restored-$(date +%s)"
  kubectl -n "$drill" create configmap restore-marker \
    --from-literal=canary="$canary" --dry-run=client -o yaml | kubectl apply -f -
  # Back up the drill namespace.
  backup="restore-drill-$(date +%s)"
  echo "Backing up namespace $drill as $backup..."
  velero backup create "$backup" --namespace "$ns" --include-namespaces "$drill" --wait || true
  bphase="$(kubectl -n "$ns" get backups.velero.io "$backup" -o jsonpath='{.status.phase}')"
  echo "Backup $backup phase: $bphase"
  if [ "$bphase" != "Completed" ]; then
    velero backup describe "$backup" --namespace "$ns" --details || true
    kubectl -n "$ns" logs deploy/velero --tail=200 || true
    kubectl delete namespace "$drill" --ignore-not-found >/dev/null 2>&1 || true
    exit 1
  fi
  # Simulate loss, then restore from the backup.
  echo "Deleting namespace $drill to simulate loss..."
  kubectl delete namespace "$drill" --wait --timeout=180s
  restore="restore-drill-$(date +%s)"
  echo "Restoring from $backup as $restore..."
  velero restore create "$restore" --namespace "$ns" --from-backup "$backup" --wait || true
  rphase="$(kubectl -n "$ns" get restores.velero.io "$restore" -o jsonpath='{.status.phase}')"
  echo "Restore $restore phase: $rphase"
  if [ "$rphase" != "Completed" ]; then
    velero restore describe "$restore" --namespace "$ns" --details || true
    kubectl -n "$ns" logs deploy/velero --tail=200 || true
    kubectl delete namespace "$drill" --ignore-not-found >/dev/null 2>&1 || true
    exit 1
  fi
  # Assert the seeded resource came back with its value.
  got="$(kubectl -n "$drill" get configmap restore-marker -o jsonpath='{.data.canary}' 2>/dev/null || true)"
  kubectl delete namespace "$drill" --ignore-not-found >/dev/null 2>&1 || true
  if [ "$got" != "$canary" ]; then
    echo "restore drill FAILED: marker not restored (got '$got', want '$canary')"; exit 1
  fi
  echo "restore drill OK: $drill restored from $backup with marker intact"

# Image security gate: fail if a PLATFORM image (one we deploy) carries a CRITICAL
# vulnerability not accepted in the merged ignore list. DOKS-managed namespaces
# (kube-system) are excluded -- those images are DigitalOcean's to patch. Gated on
# CRITICAL only; HIGH is visibility-only (trivy-operator dashboards + Renovate
# bumps) and reported here but does not fail the build, since platform images are
# third-party charts we can only fix by bumping. Reads the trivy-operator
# VulnerabilityReports (kubectl+jq, no runner-side image pulls); run after the
# stack has reconciled. Reads KUBECONFIG (falls back to ./.kubeconfig). Accepted
# CVEs are the union of a mandatory base file (TRIVYIGNORE_BASE -- the platform's
# suppressions every downstream inherits) and an optional local file
# (TRIVYIGNORE_LOCAL -- the downstream's own app-image CVEs); suppression is
# applied here so trivy-operator's own reports stay complete.
cluster-verify-image-scan:
  #!/usr/bin/env bash
  set -euo pipefail
  export KUBECONFIG="${KUBECONFIG:-.kubeconfig}"
  crd=vulnerabilityreports.aquasecurity.github.io
  base_ignore="${TRIVYIGNORE_BASE:-.trivyignore-cluster}"
  extra_ignore="${TRIVYIGNORE_LOCAL:-.trivyignore-cluster}"
  # trivy-operator scans asynchronously (a fresh cluster's first scans include a
  # trivy-DB download), so wait until the report count is non-zero and stops
  # growing (stable across two polls) before evaluating. Fail closed if no report
  # ever appears -- a gate that silently passes an unscanned cluster is worthless.
  echo "Waiting for trivy-operator VulnerabilityReports to stabilize..."
  deadline=$((SECONDS + 480))
  prev=-1
  stable=0
  while :; do
    n="$( { kubectl get "$crd" -A --no-headers 2>/dev/null || true; } | wc -l | tr -d ' ')"
    echo "  reports: $n"
    if [ "$n" -gt 0 ] && [ "$n" = "$prev" ]; then
      stable=$((stable + 1))
      [ "$stable" -ge 2 ] && break
    else
      stable=0
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      [ "$n" -eq 0 ] && { echo "no VulnerabilityReports after 480s (trivy-operator did not scan); failing closed"; exit 1; }
      echo "report count still changing after 480s; evaluating the $n reports present"
      break
    fi
    prev="$n"
    sleep 20
  done
  # DOKS-managed namespaces, excluded from the gate (images we cannot patch).
  ex='["kube-system"]'
  # Accepted CVE IDs = union of the base + optional local ignore files (deduped;
  # missing files skipped, same file listed twice collapses via unique).
  acc="$( { for f in "$base_ignore" "$extra_ignore"; do [ -f "$f" ] && cat "$f"; done; } \
    | jq -Rn '[inputs | select(test("^[[:space:]]*(#|$)") | not)] | unique')"
  reports="$(kubectl get "$crd" -A -o json)"
  # Informational (non-gating): distinct HIGH CVEs in platform images.
  high="$(jq --argjson ex "$ex" '[ .items[]
    | select((.metadata.namespace) as $ns | $ex | index($ns) | not)
    | (.report.vulnerabilities // [])[] | select(.severity=="HIGH") | .vulnerabilityID
  ] | unique | length' <<<"$reports")"
  echo "Suppressed CVEs: $(jq 'length' <<<"$acc"); HIGH CVEs in platform images (not gated): $high"
  # Gate: unsuppressed CRITICAL in platform images. One TSV row each: CVE PACKAGE NS IMAGE.
  findings="$(jq -r --argjson acc "$acc" --argjson ex "$ex" '
    .items[]
    | .metadata.namespace as $ns
    | select($ex | index($ns) | not)
    | ((.report.artifact.repository // "?") + ":" + (.report.artifact.tag // "")) as $img
    | (.report.vulnerabilities // [])[]
    | select(.severity == "CRITICAL")
    | select(.vulnerabilityID as $id | ($acc | index($id)) | not)
    | [.vulnerabilityID, (.resource // "?"), $ns, $img] | @tsv
  ' <<<"$reports" | sort -u)"
  if [ -n "$findings" ]; then
    echo "IMAGE SCAN GATE FAILED -- unsuppressed CRITICAL vulnerabilities in platform images:"
    printf 'CVE\tPACKAGE\tNAMESPACE\tIMAGE\n'
    printf '%s\n' "$findings"
    echo
    echo "Bump the offending chart to a fixed image; if no fix exists yet, add the CVE ID to your .trivyignore-cluster with a reason."
    exit 1
  fi
  echo "IMAGE SCAN GATE PASSED -- no unsuppressed CRITICAL vulnerabilities in platform images."

# Create the kind cluster (1 control-plane + 2 workers), then bootstrap the platform
# onto it. `*args` pass through to bootstrap (e.g. --grafana-cloud). Uses $DCTL (default:
# `nix run github:dantofa/platform#default --`; the platform overrides it to
# `go run ./cmd/dctl`).
cluster-local-create *args:
  #!/usr/bin/env bash
  set -euo pipefail
  # shellcheck disable=SC2206
  dctl=(${DCTL:-nix run github:dantofa/platform#default --})
  "${dctl[@]}" local cluster create --control-planes 1 --workers 2
  just cluster-local-bootstrap {{args}}

# Bootstrap the platform (Flux + ingress + the shared stacks) onto an already-created
# cluster — split from create so you can (re-)bootstrap without recreating, e.g. to
# add --grafana-cloud. Reads BWS_* for the ESO secret-zero (local needs no DO/Spaces
# creds); uses $DCTL and $BASE_DOMAIN.
cluster-local-bootstrap *args:
  #!/usr/bin/env bash
  set -euo pipefail
  : "${BWS_ACCESS_TOKEN:?set BWS_ACCESS_TOKEN (dev shell + bws login)}"
  : "${BWS_PROJECT_ID:?set BWS_PROJECT_ID}"
  : "${BWS_ORGANIZATION_ID:?set BWS_ORGANIZATION_ID}"
  base_domain="${BASE_DOMAIN:-local.dantofa.dev}"
  # shellcheck disable=SC2206
  dctl=(${DCTL:-nix run github:dantofa/platform#default --})
  "${dctl[@]}" local cluster bootstrap --base-domain "$base_domain" {{args}}

# Generic health verification of the running cluster: kubeconfig fetched, all
# nodes Ready, the whole GitOps tree reconciled, then the platform-infra checks
# (Velero backup + the image-security gate). App-level verification is the
# consumer's own compose recipe.
cluster-local-verify:
  #!/usr/bin/env bash
  set -euo pipefail
  export KUBECONFIG=.kubeconfig
  # shellcheck disable=SC2206
  dctl=(${DCTL:-nix run github:dantofa/platform#default --})
  "${dctl[@]}" local cluster connect
  kubectl wait --for=condition=Ready nodes --all --timeout=180s
  # Gate on the whole GitOps tree reconciling (generous timeout for cold pulls).
  "${dctl[@]}" flux kustomization verify --wait --timeout 600s --kubeconfig .kubeconfig
  just cluster-verify-backup
  just cluster-verify-image-scan

# Delete the cluster via the graceful teardown (drains DNS records, stops the
# ingress controller, reaps any tunnel). Uses $DCTL.
cluster-local-delete *args:
  #!/usr/bin/env bash
  set -euo pipefail
  # shellcheck disable=SC2206
  dctl=(${DCTL:-nix run github:dantofa/platform#default --})
  "${dctl[@]}" local cluster delete {{args}}

# Generic smoke: create -> verify -> delete (delete runs after, on success only).
# Consumers wanting an app-inclusive e2e write their own recipe composing these.
cluster-local-test: cluster-local-create cluster-local-verify && cluster-local-delete

# Export the local cluster's CA (the trust anchor cert-manager mints, from which
# Traefik's default cert is issued) to a file, and print how to point tools at it.
# This is what replaces per-tool skip-verify flags: one anchor, understood by every
# OpenSSL/Go/Python/Node HTTP stack via the standard CA-bundle env vars. Re-run it
# after recreating the cluster -- the CA is minted per cluster, so a fresh cluster
# means a fresh anchor. Config: ${KUBECONFIG:-.kubeconfig}, ${LOCAL_CA_FILE:-.local.pem}.
cluster-local-ca:
  #!/usr/bin/env bash
  set -euo pipefail
  export KUBECONFIG="${KUBECONFIG:-.kubeconfig}"
  out="${LOCAL_CA_FILE:-.local.pem}"
  # The CA Certificate is minted asynchronously after the cluster reconciles, so
  # give it a moment on a freshly-bootstrapped cluster rather than failing hard.
  for _ in $(seq 1 30); do
    if kubectl -n cert-manager get secret local-ca >/dev/null 2>&1; then break; fi
    sleep 2
  done
  if ! kubectl -n cert-manager get secret local-ca >/dev/null 2>&1; then
    echo "no local-ca secret in the cert-manager namespace -- is the cluster bootstrapped?" >&2
    exit 1
  fi
  kubectl -n cert-manager get secret local-ca -o jsonpath='{.data.tls\.crt}' | base64 -d > "$out"
  if [ ! -s "$out" ]; then
    echo "local-ca secret held no certificate" >&2
    exit 1
  fi
  abs="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
  echo "local CA written to $abs"
  echo
  echo "Trust it for every tool in this shell:"
  echo "  export SSL_CERT_FILE=$abs CURL_CA_BUNDLE=$abs REQUESTS_CA_BUNDLE=$abs NODE_EXTRA_CA_CERTS=$abs GIT_SSL_CAINFO=$abs"
  echo
  echo "Browsers use their own store; import once with:"
  echo "  certutil -d sql:\$HOME/.pki/nssdb -A -t 'C,,' -n dantofa-local -i $abs"

# Launch Chromium against the local cluster. The cluster publishes its ingress on
# the host's real :80/:443 and ${BASE_DOMAIN} resolves to 127.0.0.1, so the real URL
# just works — this recipe only adds what is still cluster-specific: trusting the
# self-signed Traefik cert, and a fresh --user-data-dir so the flags apply even when
# Chromium is already running. `*args` pass through (e.g. a URL to open). Assumes a
# bootstrapped local cluster. Config: ${CHROMIUM:-chromium}.
cluster-local-chromium *args:
  #!/usr/bin/env bash
  set -euo pipefail
  # shellcheck disable=SC2206
  chromium=(${CHROMIUM:-chromium})
  "${chromium[@]}" --ignore-certificate-errors --user-data-dir="$(mktemp -d)" {{args}}

# Run Playwright against the local cluster. Nothing has to be injected into the
# browser any more (the real URL resolves and connects on its own) — set
# `baseURL: https://${BASE_DOMAIN}` and `ignoreHTTPSErrors: true` in your
# playwright.config and run Playwright directly; this recipe just exports BASE_URL
# for a config that reads it. Pin @playwright/test to the flake's playwright-driver.
# `*args` pass through (e.g. `test`). Config: ${PLAYWRIGHT:-npx playwright}.
cluster-local-playwright *args:
  #!/usr/bin/env bash
  set -euo pipefail
  BASE_URL="https://${BASE_DOMAIN:-local.dantofa.dev}"
  export BASE_URL
  # shellcheck disable=SC2206
  playwright=(${PLAYWRIGHT:-npx playwright})
  "${playwright[@]}" {{args}}

# curl the local cluster ingress at its real URL. Only --insecure is still needed
# (Traefik serves a self-signed cert locally); resolution and routing need nothing.
# `*args` pass through (the URL + any flags). Assumes a bootstrapped local cluster.
cluster-local-curl *args:
  #!/usr/bin/env bash
  set -euo pipefail
  curl --insecure {{args}}

# Snapshot cluster + Flux state into ./preview-diagnostics for CI to upload as
# artifacts on a failed run (the cluster is destroyed afterwards, so this is the
# only surviving record). Everything is best-effort: a partial capture from a
# half-provisioned or unreachable cluster is still worth keeping, so this recipe
# never fails the run. Reads KUBECONFIG (falls back to ./.kubeconfig).
cluster-debug:
  #!/usr/bin/env bash
  set -uo pipefail
  out=preview-diagnostics
  mkdir -p "$out"
  export KUBECONFIG="${KUBECONFIG:-.kubeconfig}"
  if [ ! -s "$KUBECONFIG" ] || ! kubectl cluster-info >"$out/cluster-info.txt" 2>&1; then
    echo "cluster API unreachable via '$KUBECONFIG' (never provisioned or still coming up); in-cluster state not captured" \
      | tee "$out/README.txt"
    exit 0
  fi
  # Flux reconciliation state + failure reasons.
  flux get all -A >"$out/flux-get-all.txt" 2>&1 || true
  kubectl get kustomizations,helmreleases,helmrepositories,gitrepositories,ocirepositories \
    -A -o wide >"$out/flux-resources.txt" 2>&1 || true
  kubectl describe kustomizations,helmreleases -A >"$out/flux-describe.txt" 2>&1 || true
  # Secrets plumbing (ESO / cert-manager) - a common bootstrap failure point.
  kubectl get externalsecrets,clustersecretstores,clusterissuers,certificates \
    -A -o wide >"$out/secrets-resources.txt" 2>&1 || true
  kubectl describe externalsecrets,clustersecretstores -A >"$out/secrets-describe.txt" 2>&1 || true
  # cert-manager issuance chain: ClusterIssuer readiness, and the ACME
  # Certificate -> CertificateRequest -> Order -> Challenge chain that stalls
  # when DNS-01 (or the solver token secret) is not right. Plus the controller
  # log (a Running pod, so not covered by the not-settled loop below).
  kubectl get certificaterequests,orders,challenges -A -o wide \
    >"$out/cert-manager-chain.txt" 2>&1 || true
  kubectl describe clusterissuers,certificates,certificaterequests,orders,challenges -A \
    >"$out/cert-manager-describe.txt" 2>&1 || true
  kubectl -n cert-manager logs deploy/cert-manager --tail=400 \
    >"$out/logs_cert-manager.txt" 2>&1 || true
  # Workloads, scheduling, events.
  kubectl get pods -A -o wide >"$out/pods.txt" 2>&1 || true
  kubectl get nodes -o wide >"$out/nodes.txt" 2>&1 || true
  kubectl get events -A --sort-by=.lastTimestamp >"$out/events.txt" 2>&1 || true
  # Logs (current + previous container) for every pod not settled Running/Completed.
  kubectl get pods -A --no-headers 2>/dev/null \
    | awk '$4!="Running" && $4!="Completed"{print $1" "$2}' \
    | while read -r ns pod; do
        kubectl -n "$ns" logs "$pod" --all-containers --tail=200 \
          >"$out/logs_${ns}_${pod}.txt" 2>&1 || true
        kubectl -n "$ns" logs "$pod" --all-containers --previous --tail=200 \
          >"$out/logs_${ns}_${pod}_previous.txt" 2>&1 || true
      done
  # Flux reconciler logs (carry the reconcile errors themselves).
  for c in source-controller kustomize-controller helm-controller; do
    kubectl -n flux-system logs "deploy/$c" --tail=300 \
      >"$out/logs_flux-system_${c}.txt" 2>&1 || true
  done
  # Ingress + DNS path: the Ingress status carries the LoadBalancer address
  # external-dns reads, and external-dns' own log says whether it found the zone
  # and created/updated the record (a Running pod, so not covered by the loop
  # above). The cloudflare-tunnel controller is the local-cluster equivalent.
  kubectl get ingress -A -o wide >"$out/ingresses.txt" 2>&1 || true
  kubectl describe ingress -A >"$out/ingress-describe.txt" 2>&1 || true
  kubectl -n external-dns logs deploy/external-dns --tail=400 \
    >"$out/logs_external-dns.txt" 2>&1 || true
  kubectl -n tunnel logs deploy/cloudflare-tunnel-ingress-controller --tail=400 \
    >"$out/logs_tunnel-controller.txt" 2>&1 || true
  echo "diagnostics captured to $out/"
