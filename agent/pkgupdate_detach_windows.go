//go:build windows

package agent

import "os/exec"

// setDetachSysProcAttr is a no-op on Windows; package updates are not
// supported there (no apt/dnf/apk/pacman), so the apply path never runs.
func setDetachSysProcAttr(_ *exec.Cmd) {}
