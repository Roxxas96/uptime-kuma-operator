# Contributing

## Setup

```bash
mise install         # pulls go, golangci-lint, helm, kubectl, kind, pre-commit
                      # (pinned in mise.toml) — see https://mise.jdx.dev to install mise itself
pre-commit install    # activates the git pre-commit hook
```

`pre-commit run --all-files` runs everything on demand without a commit.

## Making a change

- `make test` runs unit tests and the `envtest`-backed controller suite.
- `make lint` runs `go vet` + `golangci-lint`; `pre-commit run --all-files`
  runs the full set CI enforces on top of that (`govulncheck`, `helm lint`,
  and the chart's RBAC-rendering test).
- If you touch `api/v1alpha1/monitor_types.go`, run `make manifests`
  afterwards — it regenerates `config/crd/bases/` and copies it into the
  chart's `crds/` directory. CI fails the build if these two drift.
- See [`docs/README.md`](docs/README.md) for the operator's user-facing
  behavior, and `docs/superpowers/specs/` for design history.

## Commit messages

This repo follows [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `docs:`, `ci:`, `build:`, ...) — look at `git log` for the
established style.

## Pull requests

All changes go through a PR against `main`; direct pushes are restricted.
CI (`lint`, `test`, `build`) must pass. See the root
[`README.md`](README.md#releasing) for how releases are cut once a change
lands.

By contributing, you agree your contributions are licensed under this
project's [Apache 2.0 license](LICENSE).
