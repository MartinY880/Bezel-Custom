#!/bin/bash
#
# Beszel fork agent installer (mydashbeszel).
#
# Installs the fork agent binary (with the package-update feature) as a
# root systemd service, downloading the binary from the hub itself rather
# than from GitHub. This is what the hub's "Add System" dialog hands out.
#
# Usage (as run by the Add System command):
#   install-agent-fork.sh -p <port> -k "<pubkey>" -t "<token>" -url "<hub_url>"
#
set -euo pipefail

PORT=45876
KEY=""
TOKEN=""
HUB_URL=""

while [ $# -gt 0 ]; do
  case "$1" in
    -k) shift; KEY="${1:-}" ;;
    -p) shift; PORT="${1:-}" ;;
    -t) shift; TOKEN="${1:-}" ;;
    -url) shift; HUB_URL="${1:-}" ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
  shift || true
done

if [ -z "$HUB_URL" ]; then
  echo "Error: -url <hub_url> is required" >&2
  exit 1
fi

# Installing a systemd service needs root. Self-elevate so the copied command
# works whether the target shell is root (common on Proxmox LXCs) or not.
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo bash "$0" "$@"
  fi
  echo "Error: this installer must run as root and sudo was not found." >&2
  exit 1
fi

# Map machine arch to the binary name served by the hub.
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

command -v curl >/dev/null 2>&1 || { echo "Error: curl is required" >&2; exit 1; }

BIN_DIR="/opt/beszel-agent"
BIN_PATH="$BIN_DIR/beszel-agent"
ENV_PATH="$BIN_DIR/beszel-agent.env"
UNIT_PATH="/etc/systemd/system/beszel-agent.service"

echo "Installing beszel fork agent (${ARCH}) from ${HUB_URL} ..."
mkdir -p "$BIN_DIR"

# Stop any existing agent (stock or fork) before replacing the binary.
systemctl stop beszel-agent 2>/dev/null || true

# Download the fork binary from the hub.
TMP_BIN="$(mktemp)"
if ! curl -fsSL "${HUB_URL%/}/agent-download?arch=${ARCH}" -o "$TMP_BIN"; then
  echo "Error: failed to download agent binary from ${HUB_URL%/}/agent-download?arch=${ARCH}" >&2
  rm -f "$TMP_BIN"
  exit 1
fi
install -m 755 "$TMP_BIN" "$BIN_PATH"
rm -f "$TMP_BIN"

# Write the environment file (root-only readable; holds the token).
umask 077
cat > "$ENV_PATH" <<EOF
KEY=$KEY
TOKEN=$TOKEN
LISTEN=$PORT
HUB_URL=$HUB_URL
EOF
umask 022

# Install a simple root systemd unit. Running as root (no filesystem
# hardening) is required for the package-update feature: the agent needs
# to refresh apt lists and install selected packages on request.
cat > "$UNIT_PATH" <<EOF
[Unit]
Description=Beszel Agent (mydashbeszel fork)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_PATH
ExecStart=$BIN_PATH
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Remove any stale drop-ins from a previous stock install that might pin a
# non-root user or filesystem hardening (would break apt operations).
rm -rf /etc/systemd/system/beszel-agent.service.d

systemctl daemon-reload
systemctl enable beszel-agent >/dev/null 2>&1 || true
systemctl restart beszel-agent

sleep 1
if systemctl is-active --quiet beszel-agent; then
  echo "Beszel fork agent installed and running (systemd: beszel-agent)."
else
  echo "Agent installed but not active. Check: journalctl -u beszel-agent -n 30" >&2
  exit 1
fi
