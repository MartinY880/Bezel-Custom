package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/henrygd/beszel/internal/common"
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
	applyJobTimeout            = 60 * time.Minute
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
	jobDir      string                    // where apply job/log/exit files live
	jobStatus   common.PackageApplyStatus // current/last background apply state
}

// newPkgUpdateManager returns nil if no supported package manager is found
// or checks are disabled via SKIP_PKG_UPDATES.
// dataDir is used to persist background apply job state (falls back to the
// system temp dir if empty).
func newPkgUpdateManager(dataDir string) *pkgUpdateManager {
	if skip, _ := GetEnv("SKIP_PKG_UPDATES"); skip == "true" {
		return nil
	}
	kind := detectPackageManager()
	if kind == pmNone {
		slog.Debug("Package updates disabled: no supported package manager")
		return nil
	}
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	pm := &pkgUpdateManager{
		kind:      kind,
		interval:  defaultUpdateCheckInterval,
		jobDir:    dataDir,
		jobStatus: common.PackageApplyStatus{Status: common.PkgApplyIdle},
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
	pm.recoverJob()
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
	held := aptHeldPackages(ctx)
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
		update := system.PackageUpdate{Name: name, AvailableVersion: fields[1], Held: held[name]}
		if last := fields[len(fields)-1]; len(fields) >= 6 && strings.HasSuffix(last, "]") {
			update.CurrentVersion = strings.TrimSuffix(last, "]")
		}
		updates = append(updates, update)
	}
	return updates, nil
}

// aptHeldPackages returns the set of packages pinned via apt-mark hold.
// Held packages can't be upgraded by apply, so the UI marks them unselectable.
func aptHeldPackages(ctx context.Context) map[string]bool {
	held := map[string]bool{}
	out, err := exec.CommandContext(ctx, "apt-mark", "showhold").Output()
	if err != nil {
		return held
	}
	for _, name := range strings.Fields(string(out)) {
		held[name] = true
	}
	return held
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

// --- background apply job -------------------------------------------------
//
// Applying updates runs DETACHED from the agent process: a system upgrade can
// restart the agent itself (libc updates, needrestart), and holding the
// hub<->agent request open for the whole install caused timeouts and
// "failed" reports for applies that actually succeeded. Instead, startApply
// launches the install in its own session (systemd-run transient unit when
// available so the service cgroup kill can't take it down), records the job
// to disk, and returns immediately. The hub polls applyStatus, and a restarted
// agent recovers the in-flight job from the job file.

// applyJob is the persisted metadata for a background apply.
type applyJob struct {
	Packages  []string  `json:"packages"`
	StartedAt time.Time `json:"startedAt"`
}

// startApply validates the packages and launches the install detached.
// It returns the job status (running) immediately, or an error if a job is
// already in progress or the command could not be started.
func (pm *pkgUpdateManager) startApply(packages []string) (common.PackageApplyStatus, error) {
	for _, pkg := range packages {
		if !validPackageName.MatchString(pkg) {
			return common.PackageApplyStatus{}, fmt.Errorf("invalid package name: %q", pkg)
		}
	}

	pm.Lock()
	defer pm.Unlock()
	if pm.jobStatus.Status == common.PkgApplyRunning {
		return pm.jobStatus, errors.New("a package apply is already running")
	}
	// fail fast with a clear message instead of letting apt error on holds
	requested := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		requested[pkg] = true
	}
	for _, u := range pm.updates {
		if u.Held && requested[u.Name] {
			return common.PackageApplyStatus{}, fmt.Errorf("%s is held (apt-mark hold); lift the hold to update it", u.Name)
		}
	}

	argv, err := pm.applyArgv()
	if err != nil {
		return common.PackageApplyStatus{}, err
	}
	argv = append(argv, packages...)

	// sh script: run the install, capture all output, record the exit code.
	// argv tokens are fixed commands + regex-validated package names, and the
	// log/exit paths are single-quoted, so the script is injection-safe.
	script := "export DEBIAN_FRONTEND=noninteractive; " +
		strings.Join(argv, " ") +
		" > '" + pm.applyLogPath() + "' 2>&1; echo $? > '" + pm.applyExitPath() + "'"

	_ = os.Remove(pm.applyExitPath())
	_ = os.Remove(pm.applyLogPath())

	var cmd *exec.Cmd
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		if _, err := exec.LookPath("systemd-run"); err == nil {
			// own transient unit: survives beszel-agent service restarts
			cmd = exec.Command("systemd-run", "--collect", "--quiet",
				"--unit=beszel-pkg-apply", "--property=Type=oneshot",
				"--property=RuntimeMaxSec="+fmt.Sprint(int(applyJobTimeout.Seconds())),
				"sh", "-c", script)
		}
	}
	detached := cmd != nil
	if cmd == nil {
		// fallback (OpenRC/no systemd): new session so agent restarts/signals
		// don't kill the install
		cmd = exec.Command("sh", "-c", script)
		setDetachSysProcAttr(cmd)
	}
	if err := cmd.Start(); err != nil {
		return common.PackageApplyStatus{}, fmt.Errorf("failed to start apply: %w", err)
	}
	if detached {
		// systemd-run exits as soon as the unit is started; reap it
		go func() { _ = cmd.Wait() }()
	} else {
		// reap the long-running child when the agent stays alive
		go func() { _ = cmd.Wait() }()
	}

	job := &applyJob{Packages: packages, StartedAt: time.Now()}
	pm.jobStatus = common.PackageApplyStatus{
		Status:    common.PkgApplyRunning,
		Packages:  packages,
		StartedAt: job.StartedAt.Unix(),
	}
	pm.saveJob(job)
	go pm.watchJob(job)
	slog.Info("Package apply started", "packages", len(packages))
	return pm.jobStatus, nil
}

// applyStatus returns the current/last apply job state.
func (pm *pkgUpdateManager) applyStatus() common.PackageApplyStatus {
	pm.Lock()
	defer pm.Unlock()
	return pm.jobStatus
}

// watchJob polls for the exit file the detached script writes on completion,
// then finalizes the job and refreshes the cached update list.
func (pm *pkgUpdateManager) watchJob(job *applyJob) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if data, err := os.ReadFile(pm.applyExitPath()); err == nil {
			code, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				code = -1
			}
			pm.finalizeJob(job, code)
			return
		}
		if time.Since(job.StartedAt) > applyJobTimeout {
			pm.finalizeJob(job, -1)
			return
		}
	}
}

// finalizeJob records the outcome and refreshes the cached update list so the
// hub sees the post-apply state.
func (pm *pkgUpdateManager) finalizeJob(job *applyJob, exitCode int) {
	status := common.PackageApplyStatus{
		Packages:   job.Packages,
		StartedAt:  job.StartedAt.Unix(),
		FinishedAt: time.Now().Unix(),
	}
	if exitCode == 0 {
		status.Status = common.PkgApplyDone
	} else {
		status.Status = common.PkgApplyFailed
		if exitCode == -1 && time.Since(job.StartedAt) > applyJobTimeout {
			status.Message = "apply timed out or was interrupted"
		} else {
			status.Message = pm.applyLogTail()
		}
	}
	pm.Lock()
	pm.jobStatus = status
	pm.Unlock()
	_ = os.Remove(pm.jobPath())
	slog.Info("Package apply finished", "status", status.Status)
	if err := pm.refresh(); err != nil {
		slog.Debug("Package update check failed", "err", err)
	}
}

// recoverJob resumes watching an apply that was in flight when the agent
// restarted (e.g. the upgrade restarted the agent itself).
func (pm *pkgUpdateManager) recoverJob() {
	data, err := os.ReadFile(pm.jobPath())
	if err != nil {
		return
	}
	var job applyJob
	if err := json.Unmarshal(data, &job); err != nil {
		_ = os.Remove(pm.jobPath())
		return
	}
	pm.Lock()
	pm.jobStatus = common.PackageApplyStatus{
		Status:    common.PkgApplyRunning,
		Packages:  job.Packages,
		StartedAt: job.StartedAt.Unix(),
	}
	pm.Unlock()
	slog.Info("Recovered in-flight package apply", "packages", len(job.Packages))
	go pm.watchJob(&job)
}

// applyArgv returns the install command for this package manager (without
// package args). Unprivileged agents escalate through the sudoers wrapper.
func (pm *pkgUpdateManager) applyArgv() ([]string, error) {
	if os.Geteuid() != 0 {
		wrapper := defaultApplyWrapperPath
		if wrapperEnv, exists := GetEnv("UPDATE_APPLY_WRAPPER"); exists && wrapperEnv != "" {
			wrapper = wrapperEnv
		}
		return []string{"sudo", "-n", wrapper}, nil
	}
	switch pm.kind {
	case pmApt:
		return []string{"apt-get", "install", "--only-upgrade", "-y"}, nil
	case pmDnf:
		bin := "dnf"
		if _, err := exec.LookPath(bin); err != nil {
			bin = "yum"
		}
		return []string{bin, "update", "-y"}, nil
	case pmApk:
		return []string{"apk", "add", "-u"}, nil
	case pmPacman:
		return []string{"pacman", "-S", "--noconfirm"}, nil
	}
	return nil, errors.ErrUnsupported
}

func (pm *pkgUpdateManager) jobPath() string      { return filepath.Join(pm.jobDir, "pkg-apply.json") }
func (pm *pkgUpdateManager) applyLogPath() string { return filepath.Join(pm.jobDir, "pkg-apply.log") }
func (pm *pkgUpdateManager) applyExitPath() string {
	return filepath.Join(pm.jobDir, "pkg-apply.exit")
}

func (pm *pkgUpdateManager) saveJob(job *applyJob) {
	if data, err := json.Marshal(job); err == nil {
		_ = os.WriteFile(pm.jobPath(), data, 0600)
	}
}

// applyLogTail returns the last part of the apply log for failure messages.
func (pm *pkgUpdateManager) applyLogTail() string {
	data, err := os.ReadFile(pm.applyLogPath())
	if err != nil || len(data) == 0 {
		return "apply failed (no log output)"
	}
	const maxTail = 1000
	tail := strings.TrimSpace(string(data))
	if len(tail) > maxTail {
		tail = "…" + tail[len(tail)-maxTail:]
	}
	return tail
}
