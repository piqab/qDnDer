# What's new

[Русский](WHATSNEW.md) | **English**

## 0.1.0 — first release

qdnder is an independent CI for building Docker images: your own server,
your own runners, your own web UI — no GitHub or Forgejo lock-in.

- **Image builds**: simple (repo + Dockerfile) or a `qdnder.yaml` pipeline
  with pre/post steps and tag templates.
- **Triggers**: Gitea/Forgejo/GitHub/GitLab webhooks, REST API, MCP for AI
  agents.
- **Rootless BuildKit**: no privileged containers, works in compose and
  Kubernetes.
- **Web UI and admin panel** in Russian and English: live logs, projects,
  users, tokens, runners.
- **Storage**: SQLite on a single host, PostgreSQL on a VPS or in a cluster.
- **nkt hub plugin**: builds, summary, trigger and cancel from the hub;
  installed as an archive via "Upload your own plugin".
- **Secret-free nkt integration**: semver image tags → the hub deploys them
  itself via `registry_poll`.
