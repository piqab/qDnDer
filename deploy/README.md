# Deployment

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
