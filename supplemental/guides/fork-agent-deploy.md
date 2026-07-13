# Deploying the fork agent (mydashbeszel)

The stock beszel agent has none of this fork's features (package updates).
The hub's **Add System** dialog is wired to hand out *this fork's* agent, so
any system you add from the UI gets the right binary automatically.

## How "Add System" works now

- **Linux (native/systemd)** — the copied command fetches `install-agent.sh`
  **from your hub** (not `get.beszel.dev`) and runs it. The script downloads the
  fork agent binary from the hub (`/agent-download?arch=…`), installs it to
  `/opt/beszel-agent`, and runs it as a **root** systemd service so the
  package-update feature works (checks refresh apt; apply installs directly).
  The script self-elevates with `sudo` if you aren't already root.
- **Docker** — see below; requires publishing a fork image first.

## Staging the agent binaries on the hub (one-time, after each agent rebuild)

The hub serves binaries from `<data-dir>/agents/`. After `make build` (or the
per-arch builds), copy them in:

```bash
mkdir -p <hub-data-dir>/agents
cp build/beszel-agent_linux_amd64 <hub-data-dir>/agents/
cp build/beszel-agent_linux_arm64 <hub-data-dir>/agents/   # if you have arm hosts
```

On this deployment the data dir is `/opt/beszel/beszel_data`, owned by `beszel`.
Only `amd64` and `arm64` are served (validated to prevent path traversal).

## Upgrading the existing stock agents

Re-run the same Add System → Linux command on each box that currently runs the
stock agent. The script stops the old service, drops in the fork binary, removes
any stock drop-ins that pinned a non-root user, and restarts as root. Their
system records stay the same (same token/key).

## Docker path (fork image)

The compose/run snippets use the `AGENT_IMAGE` constant in
`internal/site/src/components/install-dropdowns.tsx`, defaulting to the upstream
`henrygd/beszel-agent` (non-breaking). To make Docker agents use the fork:

1. Build & push a fork image. The repo's `.github/workflows/docker-images.yml`
   builds `ghcr.io/<owner>/<repo>/beszel-agent` (and Docker Hub if you set the
   `DOCKERHUB_*` secrets) on every `v*` tag push:
   ```bash
   git tag v0.18.3-fork.1 && git push origin v0.18.3-fork.1
   ```
   Or build locally where Docker is available:
   ```bash
   docker build -f internal/dockerfile_agent -t ghcr.io/martiny880/mydashbeszel/beszel-agent .
   docker push ghcr.io/martiny880/mydashbeszel/beszel-agent
   ```
2. Set `AGENT_IMAGE` to that image name, rebuild the hub (`make build-hub`), and
   redeploy. New Docker "Add System" snippets will reference the fork image.

Note: a Docker agent monitors the *container's* package manager (the alpine base
image), not the LXC/VM host — so for host package updates, use the native install.
