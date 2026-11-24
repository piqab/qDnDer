# Changelog

**English** | [Русский](CHANGELOG.ru.md)

All notable changes to qdnder are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/); versions
follow semver.

## [Unreleased]

### Added
- `QDNDER_RUNNER_TOKEN` environment override for the server: one compose
  `.env` can serve both server and runner (used by the nkt pipeline).
- Release workflow (`.github/workflows/release.yml`): `v*` tags build
  binaries (linux/darwin amd64+arm64, windows amd64) with SHA-256 checksums
  and publish multi-arch ghcr.io images (`qdnder`, `qdnder-runner`).
- nkt deployment pipelines: `deploy/nkt/pipeline-compose.yaml` (one compose
  stack, hub-generated runner token, `nkt-example.json` for the hub
  "Examples" menu) and `deploy/nkt/pipeline-k8s.yaml` (manifests into a
  cluster).
- nkt host script `deploy/nkt/deploy-host.nkt` (+ `pipeline-host.yaml`):
  deploy on a Docker host from the published ghcr.io images; deploy/README
  gains a "Verify it works" walkthrough.

### Fixed
- Session cookies now set `Secure` and `HttpOnly` on both login and logout
  (CodeQL alerts #1–#3). Remote UI access requires HTTPS; localhost stays
  usable over plain HTTP.
- Runner process handling is now cross-platform (`proc_unix.go` /
  `proc_windows.go`): the Windows binary build failed on Unix-only
  `syscall.Kill` / `Setpgid`.

## [0.1.0] - 2026-11-24

### Added
- Initial repository: MIT license, bilingual README.
- Core server: SQLite/PostgreSQL store, users and roles (admin/operator/viewer),
  API tokens, projects with registry/git/webhook secrets, build queue, REST API.
- Runner with rootless BuildKit (buildctl), live log streaming over SSE,
  cancellation of queued and running builds, runner heartbeats.
- `qdnder.yaml` pipelines (flat nkt-style): dockerfile/context/image/tags/
  build_args overrides, pre/post steps; tag templates {tag}, {branch},
  {sha}, {sha:N}, {date}.
- Webhooks: Gitea/Forgejo/GitHub (HMAC-SHA256) and GitLab (token), push and
  tag events.
- MCP server at /mcp (JSON-RPC 2.0): qdnder_builds, qdnder_build, qdnder_logs,
  qdnder_cancel with role enforcement.
- Embedded web UI and admin panel (Russian/English): builds with live logs,
  projects, users, tokens, runners, runner-token rotation.
- Docker Compose (server + rootless buildkitd + runner, optional PostgreSQL
  profile) and Kubernetes manifests (server + PVC or PostgreSQL DSN, rootless
  BuildKit StatefulSet, scalable runner Deployment).
- nkt hub plugin as a manually uploadable archive (protocol v1, ru/en,
  HMAC-signed calls, tests against fakes): builds table, log view, trigger
  form, cancel with confirmation, summary with findings, script command
  `qdnder …`, MCP tools; `make plugin-archive` builds tar.gz + zip.
- Secret-free nkt integration: semver image tags for hub `registry_poll`.
