package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/henrygd/beszel/internal/entities/system"
)

type packageManagerKind uint8

const (
	pmNone packageManagerKind = iota
	pmApt
	pmDnf
	pmApk
	pmPacman
)

const (
	defaultUpdateCheckInterval = 24 * time.Hour
	defaultApplyWrapperPath    = "/usr/local/bin/beszel-apply-updates"
)

// validPackageName must match every package name before it is passed to any command
var validPackageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+:_-]*$`)

// pkgUpdateManager periodically checks the host's package manager for available updates.
type pkgUpdateManager struct {
	sync.Mutex
	kind        packageManagerKind
	updates     []system.PackageUpdate
	lastChecked time.Time
	interval    time.Duration
}

// newPkgUpdateManager returns nil if no supported package manager is found
// or checks are disabled via SKIP_PKG_UPDATES.
func newPkgUpdateManager() *pkgUpdateManager {
	if skip, _ := GetEnv("SKIP_PKG_UPDATES"); skip == "true" {
		return nil
	}
	kind := detectPackageManager()
	if kind == pmNone {
		slog.Debug("Package updates disabled: no supported package manager")
		return nil
	}
	pm := &pkgUpdateManager{
		kind:     kind,
		interval: defaultUpdateCheckInterval,
	}
	// UPDATE_CHECK_INTERVAL env var to check for package updates at this interval
	if intervalEnv, exists := GetEnv("UPDATE_CHECK_INTERVAL"); exists {
		if duration, err := time.ParseDuration(intervalEnv); err == nil && duration > 0 {
			pm.interval = duration
			slog.Info("UPDATE_CHECK_INTERVAL", "duration", duration)
		} else {
			slog.Warn("Invalid UPDATE_CHECK_INTERVAL", "err", err)
		}
	}
	pm.startWorker()
	return pm
}

// detectPackageManager maps /etc/os-release ID/ID_LIKE to a package manager,
// falling back to checking for known binaries in PATH.
func detectPackageManager() packageManagerKind {
	if runtime.GOOS != "linux" {
		return pmNone
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, id := range parseOsReleaseIDs(data) {
			switch id {
			case "debian", "ubuntu", "raspbian", "linuxmint", "pop":
				return pmApt
			case "rhel", "fedora", "centos", "rocky", "almalinux", "amzn", "ol":
				return pmDnf
			case "alpine":
				return pmApk
			case "arch", "archarm", "manjaro", "endeavouros":
				return pmPacman
			}
		}
	}
	for _, candidate := range []struct {
		bin  string
		kind packageManagerKind
	}{
		{"apt-get", pmApt}, {"dnf", pmDnf}, {"yum", pmDnf}, {"apk", pmApk}, {"pacman", pmPacman},
	} {
		if _, err := exec.LookPath(candidate.bin); err == nil {
			return candidate.kind
		}
	}
	return pmNone
}

// parseOsReleaseIDs returns the ID value followed by all ID_LIKE values
func parseOsReleaseIDs(data []byte) []string {
	var ids, idLike []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			ids = append(ids, strings.ToLower(value))
		case "ID_LIKE":
			idLike = append(idLike, strings.Fields(strings.ToLower(value))...)
		}
	}
	return append(ids, idLike...)
}

func (pm *pkgUpdateManager) startWorker() {
	// initial check runs async so agent startup is never blocked
	go func() {
		if err := pm.refresh(); err != nil {
			slog.Debug("Package update check failed", "err", err)
		}
		ticker := time.NewTicker(pm.interval)
		for range ticker.C {
			if err := pm.refresh(); err != nil {
				slog.Debug("Package update check failed", "err", err)
			}
		}
	}()
}

// getUpdates returns the cached list of available updates.
func (pm *pkgUpdateManager) getUpdates() []system.PackageUpdate {
	pm.Lock()
	defer pm.Unlock()
	// refresh() replaces the slice wholesale, so sharing it is safe
	return pm.updates
}

// getUpdateCount returns the number of cached available updates.
func (pm *pkgUpdateManager) getUpdateCount() uint16 {
	pm.Lock()
	defer pm.Unlock()
	count := len(pm.updates)
	if count > 65535 {
		count = 65535
	}
	return uint16(count)
}

// refresh re-checks the package manager for available updates and replaces the cache.
func (pm *pkgUpdateManager) refresh() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var updates []system.PackageUpdate
	var err error
	switch pm.kind {
	case pmApt:
		updates, err = checkApt(ctx)
	case pmDnf:
		updates, err = checkDnf(ctx)
	case pmApk:
		updates, err = checkApk(ctx)
	case pmPacman:
		updates, err = checkPacman(ctx)
	default:
		err = errors.ErrUnsupported
	}
	if err != nil {
		return err
	}
	pm.Lock()
	pm.updates = updates
	pm.lastChecked = time.Now()
	pm.Unlock()
	slog.Debug("Package updates", "count", len(updates))
	return nil
}

func checkApt(ctx context.Context) ([]system.PackageUpdate, error) {
	// syncing package lists requires root; otherwise rely on the system's own timers
	if os.Geteuid() == 0 {
		_ = exec.CommandContext(ctx, "apt-get", "update", "-qq").Run()
	}
	out, err := exec.CommandContext(ctx, "apt", "list", "--upgradable").Output()
	if err != nil {
		return nil, err
	}
	var updates []system.PackageUpdate
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		// base-files/stable-updates 12.4+deb12u12 amd64 [upgradable from: 12.4+deb12u11]
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Listing") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, _, found := strings.Cut(fields[0], "/")
		if !found {
			continue
		}
		update := system.PackageUpdate{Name: name, AvailableVersion: fields[1]}
		if last := fields[len(fields)-1]; len(fields) >= 6 && strings.HasSuffix(last, "]") {
			update.CurrentVersion = strings.TrimSuffix(last, "]")
		}
		updates = append(updates, update)
	}
	return updates, nil
}

func checkDnf(ctx context.Context) ([]system.PackageUpdate, error) {
	bin := "dnf"
	if _, err := exec.LookPath(bin); err != nil {
		bin = "yum"
	}
	out, err := exec.CommandContext(ctx, bin, "-q", "check-update").Output()
	if err != nil {
		var exitErr *exec.ExitError
		// exit code 100 means updates are available, not an error
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 100 {
			err = nil
		} else {
			return nil, err
		}
	}
	var updates []system.PackageUpdate
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		// kernel.x86_64    5.14.0-503.el9    baseos
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "obsoleting") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		name := fields[0]
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		updates = append(updates, system.PackageUpdate{Name: name, AvailableVersion: fields[1]})
	}
	return updates, nil
}

func checkApk(ctx context.Context) ([]system.PackageUpdate, error) {
	if os.Geteuid() == 0 {
		_ = exec.CommandContext(ctx, "apk", "update", "-q").Run()
	}
	out, err := exec.CommandContext(ctx, "apk", "version", "-l", "<").Output()
	if err != nil {
		return nil, err
	}
	var updates []system.PackageUpdate
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		// musl-1.2.4-r0 < 1.2.4-r1
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "<" {
			continue
		}
		name, currentVersion := splitApkNameVersion(fields[0])
		updates = append(updates, system.PackageUpdate{
			Name:             name,
			CurrentVersion:   currentVersion,
			AvailableVersion: fields[2],
		})
	}
	return updates, nil
}

// splitApkNameVersion splits "pkg-name-1.2.3-r0" into name and version;
// the version starts at the first dash followed by a digit
func splitApkNameVersion(s string) (string, string) {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '-' && s[i+1] >= '0' && s[i+1] <= '9' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func checkPacman(ctx context.Context) ([]system.PackageUpdate, error) {
	// prefer checkupdates (pacman-contrib): no root needed, doesn't sync the real db
	if _, err := exec.LookPath("checkupdates"); err == nil {
		out, err := exec.CommandContext(ctx, "checkupdates", "--nocolor").Output()
		if err != nil {
			var exitErr *exec.ExitError
			// checkupdates exits 2 when there are no updates
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
				return nil, nil
			}
			return nil, err
		}
		return parsePacmanUpdates(out), nil
	}
	if os.Geteuid() == 0 {
		_ = exec.CommandContext(ctx, "pacman", "-Sy", "--noconfirm").Run()
	}
	out, err := exec.CommandContext(ctx, "pacman", "-Qu").Output()
	if err != nil {
		var exitErr *exec.ExitError
		// pacman -Qu exits 1 when there are no updates
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(out) == 0 {
			return nil, nil
		}
		return nil, err
	}
	return parsePacmanUpdates(out), nil
}

func parsePacmanUpdates(out []byte) []system.PackageUpdate {
	var updates []system.PackageUpdate
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		// linux 6.9.1-1 -> 6.9.2-1
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) < 4 || fields[2] != "->" {
			continue
		}
		updates = append(updates, system.PackageUpdate{
			Name:             fields[0],
			CurrentVersion:   fields[1],
			AvailableVersion: fields[3],
		})
	}
	return updates
}

// apply installs only the given packages and returns a map of package name
// to error message, where an empty string means success.
func (pm *pkgUpdateManager) apply(packages []string) (map[string]string, error) {
	for _, pkg := range packages {
		if !validPackageName.MatchString(pkg) {
			return nil, fmt.Errorf("invalid package name: %q", pkg)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Only fall back to per-package isolation for smallish batches. For large
	// selections, retrying each package individually could mean dozens of
	// sequential installs and blow past the timeout, so just report the batch
	// error against every package instead.
	const maxIsolationRetry = 25

	results := make(map[string]string, len(packages))
	output, err := pm.runApply(ctx, packages)
	switch {
	case err == nil:
		for _, pkg := range packages {
			results[pkg] = ""
		}
	case len(packages) == 1:
		results[packages[0]] = applyErrorMessage(err, output)
	case len(packages) > maxIsolationRetry:
		// too many to isolate cheaply; attribute the batch failure to all
		msg := applyErrorMessage(err, output)
		for _, pkg := range packages {
			results[pkg] = msg
		}
	default:
		// combined install failed; retry one at a time to isolate failures
		for _, pkg := range packages {
			out, err := pm.runApply(ctx, []string{pkg})
			if err != nil {
				results[pkg] = applyErrorMessage(err, out)
			} else {
				results[pkg] = ""
			}
		}
	}
	// re-check so the hub sees the new state
	if err := pm.refresh(); err != nil {
		slog.Debug("Package update check failed", "err", err)
	}
	return results, nil
}

func (pm *pkgUpdateManager) runApply(ctx context.Context, packages []string) (string, error) {
	var argv []string
	if os.Geteuid() != 0 {
		// unprivileged agents escalate through a locked-down sudoers wrapper
		wrapper := defaultApplyWrapperPath
		if wrapperEnv, exists := GetEnv("UPDATE_APPLY_WRAPPER"); exists && wrapperEnv != "" {
			wrapper = wrapperEnv
		}
		argv = []string{"sudo", "-n", wrapper}
	} else {
		switch pm.kind {
		case pmApt:
			argv = []string{"apt-get", "install", "--only-upgrade", "-y"}
		case pmDnf:
			bin := "dnf"
			if _, err := exec.LookPath(bin); err != nil {
				bin = "yum"
			}
			argv = []string{bin, "update", "-y"}
		case pmApk:
			argv = []string{"apk", "add", "-u"}
		case pmPacman:
			argv = []string{"pacman", "-S", "--noconfirm"}
		default:
			return "", errors.ErrUnsupported
		}
	}
	argv = append(argv, packages...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if pm.kind == pmApt {
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// applyErrorMessage extracts a short, useful error message from command output
func applyErrorMessage(err error, output string) string {
	msg := err.Error()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			msg = line
			break
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
