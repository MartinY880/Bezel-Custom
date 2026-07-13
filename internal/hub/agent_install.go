package hub

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pocketbase/pocketbase/core"
)

// installAgentScript is the fork agent installer served at GET /install-agent.sh.
// The "Add System" UI hands out a command that curls this and runs it on the
// target host. It downloads the fork agent binary from this hub (see
// serveAgentBinary) and installs it as a root systemd service.
//
//go:embed install-agent-fork.sh
var installAgentScript string

// allowedAgentArch restricts the arch query param to known values, so it can
// never be used to traverse outside the agents directory.
var allowedAgentArch = map[string]bool{"amd64": true, "arm64": true}

// serveInstallAgentScript serves the fork agent install script.
// Unauthenticated on purpose: the target host has no hub credentials yet, and
// the script contains no secrets (the token/key are passed as args by the
// authenticated user who copied the command from the UI).
func (h *Hub) serveInstallAgentScript(e *core.RequestEvent) error {
	e.Response.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	return e.String(http.StatusOK, installAgentScript)
}

// serveAgentBinary serves the fork agent binary for a given arch from
// <dataDir>/agents/beszel-agent_linux_<arch>. Unauthenticated, same rationale
// as the script (the binary is not secret).
func (h *Hub) serveAgentBinary(e *core.RequestEvent) error {
	arch := e.Request.URL.Query().Get("arch")
	if !allowedAgentArch[arch] {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "unsupported or missing arch (expected amd64 or arm64)"})
	}
	binPath := filepath.Join(h.DataDir(), "agents", "beszel-agent_linux_"+arch)
	if _, err := os.Stat(binPath); err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "agent binary for " + arch + " is not staged on the hub"})
	}
	e.Response.Header().Set("Content-Type", "application/octet-stream")
	e.Response.Header().Set("Content-Disposition", "attachment; filename=beszel-agent")
	http.ServeFile(e.Response, e.Request, binPath)
	return nil
}
