# Deployment

## Release binaries and images

Every `v*` tag triggers `.github/workflows/release.yml`:

- **Binaries** on [GitHub Releases](https://github.com/piqab/qDnDer/releases):
  `qdnder-linux-amd64/arm64`, `qdnder-darwin-amd64/arm64`,
  `qdnder-windows-amd64.exe` + `checksums.txt` (SHA-256).
- **Images** on ghcr.io (linux/amd64 + linux/arm64): `ghcr.io/piqab/qdnder`
  and `ghcr.io/piqab/qdnder-runner`, tags `v<X>`, `<X>` and `latest`.
  Built with the built-in `GITHUB_TOKEN` — no extra secrets.

```bash
# example: install the server binary on a Linux host
V=v0.1.0
curl -fsSL -o qdnder https://github.com/piqab/qDnDer/releases/download/$V/qdnder-linux-amd64
sudo install -m 0755 qdnder /usr/local/bin/qdnder
```

## Docker Compose (single host)

```bash
cd deploy
# 1. Start server + buildkitd first (runner needs the token):
docker compose up -d --build server buildkitd
docker compose logs server          # initial admin password
# 2. Open http://127.0.0.1:8090 → Admin → Settings → copy the runner token.
# 3. Start the runner:
QDNDER_RUNNER_TOKEN=<token> docker compose up -d --build runner
```

PostgreSQL instead of SQLite:

```bash
docker compose --profile postgres up -d postgres
# uncomment QDNDER_DB in docker-compose.yml, then:
docker compose up -d server
```

The UI is bound to `127.0.0.1:8090` — put a TLS reverse proxy in front for
remote access. BuildKit runs rootless (no privileged containers).

## Kubernetes

```bash
kubectl apply -f k8s/server.yaml
kubectl -n qdnder logs deploy/qdnder-server   # initial admin password
# Admin → Settings → runner token, then:
#   edit k8s/runner.yaml → Secret qdnder-runner → token
kubectl apply -f k8s/runner.yaml
```

- `k8s/server.yaml` — Deployment + PVC (SQLite) + Service. For PostgreSQL
  create a secret with the DSN and set `QDNDER_DB` (commented block inside).
- `k8s/runner.yaml` — rootless BuildKit StatefulSet + runner Deployment;
  scale `qdnder-runner` replicas for more parallel builds.
- Images: build and push your own from `deploy/Dockerfile` and
  `deploy/Dockerfile.runner`, then replace `ghcr.io/piqab/qdnder*:0.1.0`.

## Via nkt hub

Two ready pipelines in [`nkt/`](nkt/) — hub → Deployments → New pipeline:

- **Compose stack** — paste `nkt/pipeline-compose.yaml` (or pick "qdnder"
  from "Examples ▾" once the example is added to the hub catalog). One stack:
  server + rootless buildkitd + runner on host `ci1`. The hub generates the
  runner token (`QDNDER_RUNNER_TOKEN: generate:hex32`) and passes it to both
  server and runner through the stack `.env` — no manual token copying.
  The server accepts the token from the environment since v0.1.0.
- **Kubernetes** — paste `nkt/pipeline-k8s.yaml`: applies `k8s/server.yaml`
  and `k8s/runner.yaml` to cluster `prod` on `v*` tags. Images come from
  ghcr.io (published by the release workflow). Before the first deploy set
  the runner token in the `qdnder-runner` Secret (see the comment in the
  pipeline file).

A host-script variant (`deploy.nkt`) is planned once release binaries are
available; for now use the compose pipeline on a Docker host.

## Webhooks

Project → edit → webhook secret. Point your git hosting at:

```
https://ci.example.com/api/webhook/<project-name>
```

Gitea/Forgejo/GitHub: secret = HMAC-SHA256 (content type JSON, push events).
GitLab: secret = the plain token, Push/Tag Push events.

## nkt integration

Push semver tags (`v1.2.3`) to your registry; an nkt hub pipeline with
`registry:` + `registry_poll:` deploys new tags without any hub secrets in
qdnder. The hub plugin in `plugin/qdnder/` adds builds, summary and trigger
operations to the hub — see its README.
