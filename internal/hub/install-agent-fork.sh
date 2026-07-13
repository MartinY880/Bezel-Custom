#!/bin/sh
#
# Beszel fork agent installer (mydashbeszel).
#
# Installs the fork agent binary (with the package-update feature) as a
# root service, downloading the binary from the hub itself rather than from
# GitHub. This is what the hub's "Add System" dialog hands out.
#
# Supports both systemd (Debian/Ubuntu/etc.) and OpenRC (Alpine). Runs as
# root because the package-update feature needs to refresh the package
# manager and install selected packages on request.
#
# Usage:
#   install-agent.sh -p <port> -k "<pubkey>" -t "<token>" -url "<hub_url>"
#   install-agent.sh -u    # uninstall
#
set -eu

PORT=45876
KEY=""
TOKEN=""
HUB_URL=""
UNINSTALL=false

while [ $# -gt 0 ]; do
  case "$1" in
    -k) shift; KEY="${1:-}" ;;
    -p) shift; PORT="${1:-}" ;;
    -t) shift; TOKEN="${1:-}" ;;
    -url) shift; HUB_URL="${1:-}" ;;
    -u) UNINSTALL=true ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
  shift || true
done

if [ "$UNINSTALL" = true ]; then
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then exec sudo sh "$0" -u
    elif command -v doas >/dev/null 2>&1; then exec doas sh "$0" -u
    fi
    echo "Error: uninstall must run as root." >&2; exit 1
  fi
  echo "Uninstalling beszel fork agent..."
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl stop beszel-agent 2>/dev/null || true
    systemctl disable beszel-agent 2>/dev/null || true
    rm -f /etc/systemd/system/beszel-agent.service
    rm -rf /etc/systemd/system/beszel-agent.service.d
    systemctl daemon-reload
  fi
  if command -v rc-service >/dev/null 2>&1; then
    rc-service beszel-agent stop 2>/dev/null || true
    rc-update del beszel-agent default 2>/dev/null || true
    rm -f /etc/init.d/beszel-agent
    rm -f /var/log/beszel-agent.log /var/log/beszel-agent.err
  fi
  rm -rf /opt/beszel-agent
  echo "Beszel fork agent uninstalled."
  exit 0
fi

if [ -z "$HUB_URL" ]; then
  echo "Error: -url <hub_url> is required" >&2
  exit 1
fi

# Installing a service needs root. Self-elevate so the copied command works
# whether the target shell is root (common on LXCs) or not. Alpine typically
# ships doas rather than sudo.
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo sh "$0" -p "$PORT" -k "$KEY" -t "$TOKEN" -url "$HUB_URL"
  elif command -v doas >/dev/null 2>&1; then
    exec doas sh "$0" -p "$PORT" -k "$KEY" -t "$TOKEN" -url "$HUB_URL"
  fi
  echo "Error: must run as root, and neither sudo nor doas was found." >&2
  exit 1
fi

# Map machine arch to the binary name served by the hub.
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

# Pick a downloader (curl preferred; busybox wget as fallback on minimal boxes).
download() { # $1 = url, $2 = dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    echo "Error: neither curl nor wget is available" >&2
    return 1
  fi
}

BIN_DIR="/opt/beszel-agent"
BIN_PATH="$BIN_DIR/beszel-agent"
ENV_PATH="$BIN_DIR/beszel-agent.env"

echo "Installing beszel fork agent (${ARCH}) from ${HUB_URL} ..."
mkdir -p "$BIN_DIR"

# Detect the init system.
INIT="none"
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  INIT="systemd"
elif command -v rc-service >/dev/null 2>&1 || [ -d /etc/init.d ] && command -v openrc >/dev/null 2>&1; then
  INIT="openrc"
elif command -v rc-update >/dev/null 2>&1; then
  INIT="openrc"
fi

# Stop any existing agent (stock or fork) before replacing the binary.
if [ "$INIT" = "systemd" ]; then
  systemctl stop beszel-agent 2>/dev/null || true
elif [ "$INIT" = "openrc" ]; then
  rc-service beszel-agent stop 2>/dev/null || true
fi

# Download the fork binary from the hub.
TMP_BIN="$(mktemp)"
if ! download "${HUB_URL%/}/agent-download?arch=${ARCH}" "$TMP_BIN"; then
  echo "Error: failed to download agent binary from ${HUB_URL%/}/agent-download?arch=${ARCH}" >&2
  rm -f "$TMP_BIN"
  exit 1
fi
chmod 755 "$TMP_BIN"
mv "$TMP_BIN" "$BIN_PATH"

# Write the environment file (root-only readable; holds the token).
OLD_UMASK="$(umask)"
umask 077
cat > "$ENV_PATH" <<EOF
KEY=$KEY
TOKEN=$TOKEN
LISTEN=$PORT
HUB_URL=$HUB_URL
EOF
umask "$OLD_UMASK"

case "$INIT" in
  systemd)
    # Simple root unit (no filesystem hardening) so apt/dnf/etc. can run.
    cat > /etc/systemd/system/beszel-agent.service <<EOF
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
    # Remove stale drop-ins from a previous stock install (non-root user / hardening).
    rm -rf /etc/systemd/system/beszel-agent.service.d
    systemctl daemon-reload
    systemctl enable beszel-agent >/dev/null 2>&1 || true
    systemctl restart beszel-agent
    sleep 1
    if systemctl is-active --quiet beszel-agent; then
      echo "Beszel fork agent installed and running (systemd)."
    else
      echo "Agent installed but not active. Check: journalctl -u beszel-agent -n 30" >&2
      exit 1
    fi
    ;;
  openrc)
    # OpenRC service running as root (no command_user), env exported inline.
    cat > /etc/init.d/beszel-agent <<EOF
#!/sbin/openrc-run

name="beszel-agent"
description="Beszel Agent (mydashbeszel fork)"
command="$BIN_PATH"
command_background="yes"
pidfile="/run/\${RC_SVCNAME}.pid"
output_log="/var/log/beszel-agent.log"
error_log="/var/log/beszel-agent.err"

export KEY="$KEY"
export TOKEN="$TOKEN"
export LISTEN="$PORT"
export HUB_URL="$HUB_URL"

depend() {
    need net
    after firewall
}
EOF
    chmod +x /etc/init.d/beszel-agent
    touch /var/log/beszel-agent.log /var/log/beszel-agent.err
    rc-update add beszel-agent default >/dev/null 2>&1 || true
    rc-service beszel-agent restart
    sleep 1
    if rc-service beszel-agent status 2>/dev/null | grep -q started; then
      echo "Beszel fork agent installed and running (OpenRC)."
    else
      echo "Agent installed but not started. Check /var/log/beszel-agent.err" >&2
      exit 1
    fi
    ;;
  *)
    echo "Error: no supported init system (systemd or OpenRC) detected." >&2
    exit 1
    ;;
esac
