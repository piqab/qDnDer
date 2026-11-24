# qdnder CI — nkt hub plugin

[Русский](README.md) | **English**

This plugin adds sections and operations of the independent
[qdnder](../../README.md) CI to an [nkt](https://github.com/piqab/nkt) hub:
Docker image build list, logs, trigger and cancel, a summary with findings,
a script command and MCP tools. The plugin is **uploaded manually as an
archive** — it is not part of the nkt catalog.

## What the hub gets

- **"Builds"** — a table (project, ref, status, trigger, time); every build
  has a "Log" action (hidden text section), active ones have "Cancel" with
  confirmation; a "Build" button opens a form (project, ref, tag).
- **"Summary"** — statistics and findings: failed builds (with a jump to the
  log) and offline runners.
- **Script command**: `qdnder builds | qdnder build PROJECT [REF] |
  qdnder logs ID | qdnder cancel ID` — with validation and dry run.
- **MCP tools**: `qdnder_builds`, `qdnder_build_logs`, `qdnder_build`,
  `qdnder_cancel` (read — any token, write — a token with the "plugin qdnder"
  action).

The plugin needs no hub actions (`hub_actions: []`): qdnder only builds
images. Deployment is done by the hub itself — it watches semver tags in the
registry (`registry:` + `registry_poll:` in a pipeline), so no hub secrets
live in CI.

## Installation (archive upload)

1. Build the archive in the qdnder repository:

   ```bash
   make plugin-archive    # dist/qdnder-plugin.tar.gz and dist/qdnder-plugin.zip
   ```

   Or manually: `tar czf qdnder-plugin.tar.gz -C plugin qdnder`.

2. Hub → About → Plugins → Catalog → **"Upload your own plugin"** → the
   archive. The hub checks the package statically (version = `VERSION` in
   code, ru/en texts, secret settings, sizes, paths) and shows the full code.
3. Tick "Code reviewed" → "Save". The plugin appears in the catalog marked
   "own".
4. "Install on host" → the host that can reach the qdnder server (usually
   the hub machine or the qdnder host itself) → settings:
   - **qdnder address** — e.g. `http://127.0.0.1:8090`;
   - **qdnder API token** — a token with the `operator` role (qdnder →
     Admin → API tokens). The secret is kept only in the service settings
     file (0600).
5. "Connect" — sections, the command and the tools appear in the hub.

Updating: bump `version` in `deploy/nkt-plugin.json` and `VERSION` in
`deploy/nkt-plugin.py`, rebuild the archive, upload and "Update on host".

## Testing

```bash
python3 plugin/qdnder/deploy/test_plugin.py   # tests against fakes
```

## Security

- The plugin listens on localhost only; the hub reaches it over SSH.
- Every call is HMAC-SHA256 signed (`NKT_PLUGIN_SECRET`); timestamps older
  than 5 minutes are rejected.
- Python standard library only; arguments are validated with regular
  expressions before use; no secrets in the log.
