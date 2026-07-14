package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/henrygd/beszel/internal/common"
	"github.com/henrygd/beszel/internal/ghupdate"
)

// selfUpdateFromHub downloads the fork agent binary the hub serves, verifies
// its checksum, swaps it over the running executable, and restarts the
// service. Unlike agent/update.go (the CLI `beszel-agent update`, which pulls
// from GitHub releases), this updates from the user's own hub, so it works
// for fork builds that never hit GitHub.
func selfUpdateFromHub(req common.AgentUpdateRequest) (common.AgentUpdateResponse, error) {
	if runtime.GOOS == "windows" {
		return common.AgentUpdateResponse{}, errors.ErrUnsupported
	}

	arch := runtime.GOARCH
	wantSum, ok := req.Checksums[arch]
	if !ok || wantSum == "" {
		return common.AgentUpdateResponse{}, fmt.Errorf("hub has no staged binary for %s", arch)
	}

	exePath, err := os.Executable()
	if err != nil {
		return common.AgentUpdateResponse{}, err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return common.AgentUpdateResponse{}, err
	}

	// already running the staged build? (hash the on-disk binary)
	if currentSum, err := fileSha256(exePath); err == nil && currentSum == wantSum {
		return common.AgentUpdateResponse{Updated: false, Message: "agent is already up to date"}, nil
	}

	// prefer the agent's own HUB_URL (known reachable — it's how we connect);
	// fall back to the URL the hub sent
	baseURL := req.URL
	if envURL, _ := GetEnv("HUB_URL"); envURL != "" {
		baseURL = envURL
	}
	if baseURL == "" {
		return common.AgentUpdateResponse{}, errors.New("no hub URL available for download")
	}
	downloadURL := strings.TrimSuffix(baseURL, "/") + "/agent-download?arch=" + arch

	// download next to the current binary so the rename is atomic (same fs)
	tmpPath := exePath + ".update"
	if err := downloadFile(downloadURL, tmpPath); err != nil {
		return common.AgentUpdateResponse{}, fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpPath)

	gotSum, err := fileSha256(tmpPath)
	if err != nil {
		return common.AgentUpdateResponse{}, err
	}
	if gotSum != wantSum {
		return common.AgentUpdateResponse{}, fmt.Errorf("checksum mismatch (got %s, want %s)", gotSum[:12], wantSum[:12])
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return common.AgentUpdateResponse{}, err
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		return common.AgentUpdateResponse{}, fmt.Errorf("failed to replace binary (agent running as non-root?): %w", err)
	}
	_ = ghupdate.HandleSELinuxContext(exePath)

	slog.Info("Agent binary updated from hub, restarting", "sha256", gotSum[:12])

	// restart AFTER the response has time to reach the hub
	go func() {
		time.Sleep(2 * time.Second)
		if r := detectRestarter(); r != nil {
			if err := r.Restart(); err == nil {
				return
			}
			slog.Warn("Service restart failed, exiting for supervisor restart")
		}
		// no init system restarter (e.g. container): exit and let the
		// supervisor/restart policy bring up the new binary
		os.Exit(0)
	}()

	return common.AgentUpdateResponse{Updated: true, Message: "updated, restarting"}, nil
}

// rebootHost schedules a host reboot in 2 seconds (so the response reaches
// the hub first). Requires root.
func rebootHost() error {
	if runtime.GOOS != "linux" {
		return errors.ErrUnsupported
	}
	if os.Geteuid() != 0 {
		return errors.New("reboot requires the agent to run as root")
	}
	rebootCmd := []string{"reboot"}
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		rebootCmd = []string{"systemctl", "reboot"}
	}
	if _, err := exec.LookPath(rebootCmd[0]); err != nil {
		return fmt.Errorf("no reboot command available: %w", err)
	}
	slog.Info("Reboot requested by hub, rebooting in 2s")
	go func() {
		time.Sleep(2 * time.Second)
		if err := exec.Command(rebootCmd[0], rebootCmd[1:]...).Run(); err != nil {
			slog.Error("Reboot command failed", "err", err)
		}
	}()
	return nil
}

func fileSha256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
