# qdnder

**English** | [Русский](README.ru.md)

Independent CI service for building Docker images — like GitHub Actions or a
Forgejo runner, but self-contained: your own server, your own runners, your own
web UI. No dependency on any git hosting vendor.

One Go binary, two modes:

```
qdnder server   control plane: REST API, web UI, admin panel, MCP server
qdnder runner   worker: clone → BuildKit build → push to an external registry
```

- **Two ways to build**: a simple build (repo + Dockerfile → image) or a flat
  `qdnder.yaml` pipeline in the repository (pre/post steps, tag templates,
  build args) — same style as nkt `pipeline.yaml`.
- **Triggers**: webhooks (Gitea / Forgejo / GitHub / GitLab, HMAC-verified),
  REST API with scoped tokens, and an MCP server for AI agents.
- **Rootless BuildKit** — no privileged containers, works in Docker Compose
  and Kubernetes.
- **nkt-friendly**: push semver tags (`v1.2.3`) and an
  [nkt](https://github.com/piqab/nkt) hub deploys them via `registry_poll` —
  no hub secrets in CI. A manually uploadable nkt hub plugin is included.
- **Storage**: SQLite by default (single host), PostgreSQL via DSN (VPS /
  cluster).
- **Auth**: local users with roles (admin / operator / viewer) + API tokens.
  No OIDC.
- **Web UI and admin panel** embedded in the binary, Russian and English.

## Quick start (Docker Compose)

```bash
cd deploy
docker compose up -d --build
docker compose logs server   # initial admin password
```

Open http://localhost:8090 — log in, create a project (repo URL, image name,
registry credentials), press **Build**. Runners pick jobs up automatically.

## Quick start (binary)

```bash
make build
QDNDER_ADDR=127.0.0.1:8090 ./qdnder server &   # prints admin password on first run
QDNDER_SERVER=http://127.0.0.1:8090 \
QDNDER_RUNNER_TOKEN=<token from admin panel> \
BUILDKIT_HOST=tcp://127.0.0.1:1234 ./qdnder runner
```

The runner needs `git` and `buildctl` on PATH and a reachable BuildKit daemon
(`buildkitd --addr tcp://127.0.0.1:1234` or the compose `buildkitd` service).

## qdnder.yaml

Optional file in the repository root; overrides project defaults:

```yaml
dockerfile: Dockerfile
context: .
image: registry.example.com/team/app
tags: [latest, "{tag}", "{sha:12}"]
build_args:
  VERSION: "{tag}"
steps:
  - name: tests
    run: ./run-tests.sh
    when: before        # before | after the image build
```

Tag templates: `{tag}`, `{branch}`, `{sha}`, `{sha:N}`, `{date}`.
Without the file the project settings are used as-is.

## Deployment

- **Docker Compose**: [`deploy/docker-compose.yml`](deploy/docker-compose.yml)
  — server + rootless buildkitd + runner; optional PostgreSQL profile.
- **Kubernetes**: [`deploy/k8s/`](deploy/k8s/) — server Deployment + PVC
  (SQLite) or PostgreSQL DSN, rootless BuildKit StatefulSet, runner Deployment.
- **nkt hub plugin**: [`plugin/qdnder/`](plugin/qdnder/) — build
  `make plugin-archive`, then hub → About → Plugins → Catalog →
  **Upload your own plugin** (`qdnder-plugin.tar.gz` or `.zip`).

## API and MCP

REST API under `/api` with `Authorization: Bearer <token>`; MCP endpoint at
`/mcp` (JSON-RPC 2.0): tools `qdnder_builds`, `qdnder_build`, `qdnder_logs`,
`qdnder_cancel`. See README sections generated in the admin panel
(**Settings → API**) for the runner token and webhook URLs.

## Development

```bash
make build   # go build
make test    # go test ./... + plugin tests
```

## License

MIT — [LICENSE](LICENSE).
