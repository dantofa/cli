# dantofa platform — devbox plugin

`plugin.json` packages the platform's cluster-ops surface for **downstream
projects that use [devbox](https://www.jetify.com/devbox)**. Including it gives a
project, in one step:

- **The toolchain on PATH** — `dctl` (from the platform flake) plus
  `kubectl`/`velero`/`flux`/`jq`/`just` (the `cluster-toolchain` flake output,
  pinned to the platform's nixpkgs).
- **The `cluster.just` module + base `.trivyignore`** copied into `./.just` by the
  plugin's `init_hook` (from the `cluster-just` / `trivyignore-base` flake
  outputs), so recipes stay rev-pinned via your `devbox.lock`.
- **Config env** — `DCTL=dctl` and `TRIVYIGNORE_BASE=.just/.trivyignore-base`.

## Use it

In your project's `devbox.json`:

```json
{
  "include": ["github:dantofa/platform?dir=devbox"]
}
```

Then in your `justfile`:

```just
import '.just/cluster.just'

# Your own compose recipe over the shared primitives:
e2e:
  just cluster local create
  just deploy-my-app
  just test-my-app
  just cluster local delete
```

Now `just cluster debug`, `just cluster verify backup|restore|image-scan`, and
`just cluster local create|verify|delete|test` are available. `cluster verify
image-scan` merges the platform's base accepted-CVE list with your project's own
optional `./.trivyignore-cluster`.

- **Gitignore `./.just/`** — it is regenerated from the pinned flake outputs on
  every `devbox` shell entry.
- **Update** the module + toolchain with `devbox update` (rev-pinned in
  `devbox.lock` in between).
- Override config per project by setting `BASE_DOMAIN` (and, if needed, `DCTL`)
  in your `devbox.json` `env`.

The recipes are **primitives** — they never call back into your recipes; compose
your own end-to-end flow (create → deploy+test your app → delete) as above.

## Smoke test

`smoke.sh` + `.github/workflows/devbox.yml` stand up a synthetic consumer in CI
(this plugin, with its flake refs rewritten to the checkout) and assert the
toolchain lands on PATH, the files materialize, and `just cluster …` dispatches —
so the plugin is validated on every change without needing a real downstream repo.
