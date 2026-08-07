# dantofa platform

`dctl` — the dantofa platform control CLI (and, in time, its operator). An opinionated bootstrapping tool for
DigitalOcean and local Kubernetes clusters and components.

It deploys a fixed platform toolset via Flux:

- **Base components:** cert-manager, External Secrets Operator, Velero, Kyverno, Trivy Operator.
- **DOKS:** Cloudflare Tunnel controller by default (outbound-only, no LoadBalancer); with `--dolb`, Traefik + external-dns behind a DO LoadBalancer instead.
- **kind (local):** Cloudflare Tunnel controller, SeaweedFS.
- **With `--monitoring`:** kube-prometheus-stack (Prometheus, Alertmanager, Grafana, node-exporter, kube-state-metrics) + Grafana Alloy.

## Install

Distributed as a **Nix flake** (not published to a package index).

Run it once:

```bash
nix run github:dantofa/platform -- --help
```

Pin a commit by appending it: `github:dantofa/platform/<rev>`.

Consume it as a flake input:

```nix
inputs.dantofa-platform.url = "github:dantofa/platform";
# per system: dantofa-platform.packages.${system}.default   # the wrapped dctl
```

## Usage

```bash
$ dctl --help
$ dctl --version
```

## Just

The project also ships a reusable [`just`](https://github.com/casey/just)
module — `cluster.just` — that downstream projects can import to operate any
cluster they provision:

```bash
just cluster debug                              # snapshot cluster + Flux state
just cluster verify backup|restore|image-scan   # verify platform infra
just cluster local  create|verify|delete|test   # kind cluster lifecycle
```

Two ways to consume it, both rev-pinned via your lockfile:

- **devbox** — `include` the plugin:

  ```json
  { "include": ["github:dantofa/platform?dir=devbox"] }
  ```

- **Nix flake** — the module and base ignore list are flake outputs
  (`packages.cluster-just`, `packages.trivyignore-base`); materialize them into
  `./.just` in your dev shell.

Either way `.just/cluster.just` lands in your project; import it and compose your
own end-to-end flow over the primitives:

```just
import '.just/cluster.just'

# Your own compose recipe over the shared primitives:
e2e:
  just cluster local create
  just deploy-my-app
  just test-my-app
  just cluster local delete
```

Config comes from the environment (`DCTL`, `BASE_DOMAIN`, `TRIVYIGNORE_BASE` /
`TRIVYIGNORE_LOCAL`) with sane defaults.

## Development

Requires [Nix](https://nixos.org/) with flakes. Enter the dev shell with `nix develop` (or
[`direnv`](https://direnv.net/)) — this also installs the pre-commit hook. Copy
`.env.example` to `.env` for local secrets (e.g. the Bitwarden access token); the
shell loads it automatically.

Common tasks run via [`just`](https://github.com/casey/just):

```bash
just run [args]    # go run ./cmd/dctl
just test          # go test ./...
just build         # build dist/dctl (version-stamped)
just lint          # gofumpt, go vet, golangci-lint, deadcode, actionlint, yamllint
just format        # gofumpt -w
just sast          # govulncheck
```

See [CLAUDE.md](CLAUDE.md) for contributor conventions and project constraints.
