package hub

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/apis"
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

// stagedAgentChecksums returns arch -> sha256 hex for the agent binaries
// staged in <dataDir>/agents. Arches with no staged binary are omitted.
func (h *Hub) stagedAgentChecksums() map[string]string {
	sums := make(map[string]string, len(allowedAgentArch))
	for arch := range allowedAgentArch {
		f, err := os.Open(filepath.Join(h.DataDir(), "agents", "beszel-agent_linux_"+arch))
		if err != nil {
			continue
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, f); err == nil {
			sums[arch] = hex.EncodeToString(hash.Sum(nil))
		}
		f.Close()
	}
	return sums
}

// updateAgent handles POST /api/beszel-ext/systems/{id}/agent/update.
// Tells the agent to fetch the staged fork binary from this hub and restart.
func (h *Hub) updateAgent(e *core.RequestEvent) error {
	system, err := h.sm.GetSystem(e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("system not found", nil)
	}
	checksums := h.stagedAgentChecksums()
	if len(checksums) == 0 {
		return apis.NewApiError(http.StatusServiceUnavailable, "no agent binaries staged on the hub (data dir /agents)", nil)
	}
	result, err := system.UpdateAgentFromHub(h.appURL, checksums)
	if err != nil {
		msg := err.Error()
		// old fork agents don't know the UpdateAgent action
		if len(msg) > 0 && (msg == "unknown action: 9" || msg == "unsupported operation") {
			msg = "this agent is too old for remote update - reinstall it once with the Add System command"
		}
		return apis.NewApiError(http.StatusBadGateway, "agent update failed: "+msg, nil)
	}
	return e.JSON(http.StatusOK, result)
}

// updateAllAgents handles POST /api/beszel-ext/agents/update-all.
// Pushes the staged fork binary to every "up" system concurrently and returns
// a per-system result map: "updated", "up to date", or the error message.
func (h *Hub) updateAllAgents(e *core.RequestEvent) error {
	checksums := h.stagedAgentChecksums()
	if len(checksums) == 0 {
		return apis.NewApiError(http.StatusServiceUnavailable, "no agent binaries staged on the hub (data dir /agents)", nil)
	}
	records, err := h.FindRecordsByFilter("systems", "status = 'up'", "", 500, 0)
	if err != nil {
		return apis.NewApiError(http.StatusInternalServerError, err.Error(), nil)
	}

	type outcome struct{ name, result string }
	results := make(chan outcome, len(records))
	var wg sync.WaitGroup
	// bounded concurrency so one slow agent doesn't serialize the rest
	sem := make(chan struct{}, 5)
	for _, rec := range records {
		wg.Add(1)
		go func(id, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			system, err := h.sm.GetSystem(id)
			if err != nil {
				results <- outcome{name, "error: " + err.Error()}
				return
			}
			res, err := system.UpdateAgentFromHub(h.appURL, checksums)
			switch {
			case err != nil:
				msg := err.Error()
				if strings.Contains(msg, "unknown action") {
					msg = "agent too old for remote update - reinstall once"
				}
				results <- outcome{name, "error: " + msg}
			case res.Updated:
				results <- outcome{name, "updated"}
			default:
				results <- outcome{name, "up to date"}
			}
		}(rec.Id, rec.GetString("name"))
	}
	wg.Wait()
	close(results)

	out := make(map[string]string, len(records))
	for o := range results {
		out[o.name] = o.result
	}
	return e.JSON(http.StatusOK, map[string]any{"results": out})
}

// rebootSystem handles POST /api/beszel-ext/systems/{id}/reboot.
func (h *Hub) rebootSystem(e *core.RequestEvent) error {
	system, err := h.sm.GetSystem(e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("system not found", nil)
	}
	if err := system.RebootHostViaAgent(); err != nil {
		return apis.NewApiError(http.StatusBadGateway, "reboot failed: "+err.Error(), nil)
	}
	return e.JSON(http.StatusOK, map[string]string{"status": "rebooting"})
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
