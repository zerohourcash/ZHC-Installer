package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const defaultMegaLink = "https://mega.nz/file/tzICFL5C#8avoKJxzjLjfgj2SbhBrqMo-FCqt-i2myM1XQZy49Gg"
const defaultGithubReleaseBaseURL = "https://github.com/zerohourcash/ZHC-Installer/releases/download/v0.2.2"
const defaultGithubPartCount = 10
const defaultZeroscanURL = "https://zeroscan.io/installer/downloads/zhcash-node-seed.zip"
const defaultOutputName = "zhcash-node-seed.zip"
const snapshotSizeBytes int64 = 11172882508
const snapshotSHA256 = "20e9551f7bb35564d5f56b6ec0c908e3d23ba419eb1cc3ad266260c2857ebcf7"
const dataDirVariable = "ZHCASH_DATA_DIR"
const nodeDirVariable = "ZHCASH_NODE_DIR"
const yandexURLVariable = "ZHCASH_YANDEX_SNAPSHOT_URL"
const nodeReleaseBaseURL = "https://github.com/zerohourcash/zerohourcash/releases/download/v1.0.0"
const windowsNodeArchive = "zhcash-evolution-1.0.0-win64.zip"
const windowsNodeSHA256 = "a84a4811f78ad6f39de410145e454e5350e44480ee3708a2a751b5ebee86581c"
const linuxNodeArchive = "zhcash-evolution-1.0.0-linux-x86_64.tar.gz"
const linuxNodeSHA256 = "b6e23a66d3bb6159d68fd430de9414c3e2a0fab16d59944c436fcae08446e55b"
const zerohourdServicePath = "/etc/systemd/system/zerohourd.service"

var yandexURLPayload string
var yandexURLKey string
var waitAtExit = true

var errIdleTimeout = errors.New("download idle timeout")

type sourceKind string

const (
	sourceMega     sourceKind = "mega"
	sourceYandex   sourceKind = "yandex"
	sourceGithub   sourceKind = "github"
	sourceZeroscan sourceKind = "zeroscan"
)

type sourceConfig struct {
	Name string
	Kind sourceKind
	URL  string
}

type megaCryptoParams struct {
	Key []byte
	IV  []byte
}

type megaFileInfo struct {
	Size        int64
	DownloadURL string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nERROR: %v\n", err)
		if waitAtExit {
			waitForEnter()
		}
		os.Exit(1)
	}
	if waitAtExit {
		waitForEnter()
	}
}

func run() error {
	sourceFlag := flag.String("source", "auto", "download source: auto, yandex, mega, github, or zeroscan")
	force := flag.Bool("force", false, "delete existing output/partial file and start over")
	skipNode := flag.Bool("skip-node", false, "skip ZHCASH node release download")
	skipSnapshot := flag.Bool("skip-snapshot", false, "skip Snapshot download and extraction")
	noClean := flag.Bool("no-clean", false, "do not clean old blockchain data before extracting Snapshot")
	keepSnapshotArchive := flag.Bool("keep-snapshot-archive", false, "keep zhcash-node-seed.zip after successful extraction")
	idleTimeout := flag.Duration("idle-timeout", 5*time.Minute, "switch/retry when download makes no progress for this duration")
	sourceRetries := flag.Int("source-retries", 2, "retry attempts on the same source before switching to the next mirror")
	nodeStartTimeout := flag.Duration("node-start-timeout", defaultNodeStartupTimeout, "maximum time to wait for the installed node RPC to become ready")
	noInstallTelemetry := flag.Bool("no-install-telemetry", false, "disable installer success/failure telemetry")
	waitOnExit := flag.Bool("wait-on-exit", true, "wait for Enter before exiting")
	noWaitOnExit := flag.Bool("no-wait-on-exit", false, "do not wait for Enter before exiting")
	env := getenvMap()
	linuxServer := isLinuxServer(runtime.GOOS, env)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	runDir := filepath.Dir(exe)
	defaultDatadir, err := defaultDataDir(runtime.GOOS, env)
	if err != nil {
		defaultDatadir = ""
	}
	defaultNodedir, err := defaultNodeDir(runtime.GOOS, env, runDir)
	if err != nil {
		defaultNodedir = runDir
	}
	dataDir := flag.String("datadir", defaultDatadir, "ZHCASH data directory")
	nodeDir := flag.String("node-dir", defaultNodedir, "node release install/download directory")
	flag.Parse()
	waitAtExit = *waitOnExit && !*noWaitOnExit

	if err := ensureEnvironmentVariables(runtime.GOOS, env, *dataDir, *nodeDir); err != nil {
		return err
	}

	sources, err := resolveSources(*sourceFlag)
	if err != nil {
		return err
	}

	fmt.Println("ZHCASH Installer")
	fmt.Println("OS:", runtime.GOOS+"/"+runtime.GOARCH)
	fmt.Println("Data directory:", *dataDir)
	fmt.Println("Node directory:", *nodeDir)
	fmt.Println("Source mode:", *sourceFlag)
	if runtime.GOOS == "linux" {
		if linuxServer {
			fmt.Println("Linux mode: server/headless")
		} else {
			fmt.Println("Linux mode: GUI")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	if err := stopRunningNodes(runtime.GOOS); err != nil {
		return err
	}

	if *dataDir == "" {
		return errors.New("data directory is empty; pass --datadir")
	}
	if *nodeDir == "" {
		return errors.New("node directory is empty; pass --node-dir")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(*nodeDir, 0o755); err != nil {
		return err
	}

	if !*skipSnapshot {
		configurationBackups, err := captureConfigurationFiles(*dataDir)
		if err != nil {
			return err
		}
		snapshotPath := filepath.Join(*dataDir, defaultOutputName)
		useExistingSnapshot := false
		if *force {
			_ = os.Remove(snapshotPath)
			_ = os.Remove(snapshotPartPath(snapshotPath))
		} else {
			var err error
			useExistingSnapshot, err = prepareExistingSnapshotArchive(snapshotPath, snapshotSizeBytes, snapshotSHA256)
			if err != nil {
				return err
			}
		}
		if !*noClean {
			fmt.Println()
			fmt.Println("==> CLEAN BLOCKCHAIN DATA")
			removed, err := cleanBlockchainData(*dataDir)
			if err != nil {
				return err
			}
			fmt.Printf("Removed %d old blockchain entries; wallet and configuration files were preserved.\n", removed)
		}
		if useExistingSnapshot {
			fmt.Println("Using existing verified Snapshot archive:", snapshotPath)
		} else {
			var err error
			snapshotPath, err = downloadSnapshot(ctx, sources, *dataDir, false, *idleTimeout, *sourceRetries)
			if err != nil {
				return err
			}
		}
		fmt.Println()
		fmt.Println("==> PREPARE FOR SNAPSHOT EXTRACTION")
		if err := stopRunningNodes(runtime.GOOS); err != nil {
			return err
		}
		if !*noClean {
			removed, err := cleanBlockchainData(*dataDir)
			if err != nil {
				return err
			}
			fmt.Printf("Removed %d extra data entries before extraction; wallet, configuration, and Snapshot files were preserved.\n", removed)
		}
		fmt.Println()
		fmt.Println("==> EXTRACT SNAPSHOT")
		fmt.Println("Archive:", snapshotPath)
		extractErr := extractZipArchive(snapshotPath, *dataDir)
		if err := restoreConfigurationFiles(*dataDir, configurationBackups); err != nil {
			return err
		}
		if extractErr != nil {
			return extractErr
		}
		if err := verifySnapshotLayout(*dataDir); err != nil {
			return err
		}
		fmt.Println("Snapshot extracted and verified.")
		if shouldRemoveSnapshotArchive(*keepSnapshotArchive) {
			if err := removeSnapshotArchive(snapshotPath); err != nil {
				return err
			}
		} else {
			fmt.Println("Keeping Snapshot archive:", snapshotPath)
		}
	}

	if _, err := configureOptimizedNode(*dataDir); err != nil {
		return err
	}

	if !*skipNode {
		nodeStartedAt := time.Now()
		var startErr error
		if err := installNodeRelease(ctx, runtime.GOOS, *nodeDir, *idleTimeout, linuxServer); err != nil {
			return err
		}
		if linuxServer {
			startErr = configureZerohourdSystemd(*dataDir, *nodeDir)
		} else {
			startErr = startNodeIfNotRunning(runtime.GOOS, *nodeDir, *dataDir)
		}
		if startErr != nil {
			return reportInstallerNodeFailure(*dataDir, nodeStartedAt, startErr, *noInstallTelemetry)
		}

		fmt.Println()
		fmt.Println("==> VERIFY ZHCASH NODE STARTUP")
		fmt.Printf("Waiting up to %s for local RPC readiness. The installer remains responsive while the node initializes.\n", *nodeStartTimeout)
		ready, readyErr := waitForLocalNodeReady(context.Background(), *dataDir, *nodeStartTimeout, defaultNodeStartupPollInterval, os.Stdout)
		if readyErr != nil {
			return reportInstallerNodeFailure(*dataDir, nodeStartedAt, readyErr, *noInstallTelemetry)
		}
		elapsed := time.Since(nodeStartedAt)
		fmt.Printf("ZHCASH node is ready: height=%d, connections=%d, best block=%s, startup=%s.\n", ready.BlockHeight, ready.Connections, ready.BestBlockHash, elapsed.Round(time.Second))
		if !*noInstallTelemetry {
			payload := newInstallerTelemetryPayload("success", elapsed, ready, nil, nil)
			telemetryCtx, telemetryCancel := context.WithTimeout(context.Background(), 10*time.Second)
			telemetryErr := sendInstallerTelemetry(telemetryCtx, defaultInstallerTelemetryURL, payload)
			telemetryCancel()
			if telemetryErr != nil {
				fmt.Println("WARNING: node is ready, but installation telemetry could not be delivered:", telemetryErr)
			} else {
				fmt.Println("Installation success was logged through wallet.zeroscan.st.")
			}
		}
	}

	fmt.Println()
	fmt.Println("Finished.")
	return nil
}

func resolveSources(mode string) ([]sourceConfig, error) {
	yandex := sourceConfig{Name: "yandex", Kind: sourceYandex, URL: configuredYandexURL()}
	all := []sourceConfig{
		yandex,
		{Name: "mega", Kind: sourceMega, URL: defaultMegaLink},
		{Name: "github", Kind: sourceGithub, URL: defaultGithubReleaseBaseURL},
		{Name: "zeroscan", Kind: sourceZeroscan, URL: defaultZeroscanURL},
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return all, nil
	case "mega":
		return []sourceConfig{all[1]}, nil
	case "yandex", "ya", "яндекс":
		return []sourceConfig{all[0]}, nil
	case "github", "gh":
		return []sourceConfig{all[2]}, nil
	case "zeroscan", "zeroscan.io":
		return []sourceConfig{all[3]}, nil
	default:
		return nil, fmt.Errorf("unknown source %q; use auto, yandex, mega, github, or zeroscan", mode)
	}
}

func defaultDataDir(goos string, env map[string]string) (string, error) {
	if configured := strings.TrimSpace(env[dataDirVariable]); configured != "" {
		return configured, nil
	}
	switch goos {
	case "windows":
		if env["APPDATA"] == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(env["APPDATA"], "ZHCASH"), nil
	case "darwin":
		if env["HOME"] == "" {
			return "", errors.New("HOME is not set")
		}
		return filepath.Join(env["HOME"], "Library", "Application Support", "ZHCASH"), nil
	default:
		if env["HOME"] == "" {
			return "", errors.New("HOME is not set")
		}
		return filepath.Join(env["HOME"], ".zerohour"), nil
	}
}

func waitForEnter() {
	fmt.Println()
	fmt.Print("Press Enter to exit...")
	_, _ = fmt.Scanln()
}

func nodeProcessNames(goos string) []string {
	switch goos {
	case "windows":
		return []string{"zerohour-qt.exe", "zerohourd.exe"}
	default:
		return []string{"zerohour-qt", "zerohourd"}
	}
}

func nodeRuntimeProcessNames(goos string) []string {
	return nodeProcessNames(goos)
}

func isLinuxServer(goos string, env map[string]string) bool {
	if goos != "linux" {
		return false
	}
	for _, key := range []string{"XDG_CURRENT_DESKTOP", "DESKTOP_SESSION", "GDMSESSION", "WAYLAND_DISPLAY"} {
		if strings.TrimSpace(env[key]) != "" {
			return false
		}
	}
	return true
}

func stopRunningNodes(goos string) error {
	if err := stopManagedNodeService(goos); err != nil {
		return err
	}
	names := nodeProcessNames(goos)
	fmt.Println("Checking running ZHCASH node processes...")
	for _, name := range names {
		running, err := isProcessRunning(goos, name)
		if err != nil {
			fmt.Printf("Warning: could not check process %s: %v\n", name, err)
			continue
		}
		if !running {
			continue
		}
		fmt.Println("Stopping running process:", name)
		if err := terminateProcess(goos, name); err != nil {
			return err
		}
		for i := 0; i < 10; i++ {
			time.Sleep(time.Second)
			stillRunning, err := isProcessRunning(goos, name)
			if err != nil {
				return err
			}
			if !stillRunning {
				fmt.Println("Stopped:", name)
				break
			}
			if i == 9 {
				return fmt.Errorf("%s is still running; close it manually and run installer again", name)
			}
		}
	}
	return nil
}

func stopManagedNodeService(goos string) error {
	if goos != "linux" {
		return nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	if err := exec.Command("systemctl", "is-active", "--quiet", "zerohourd.service").Run(); err != nil {
		return nil
	}
	fmt.Println("Stopping systemd service: zerohourd.service")
	output, err := exec.Command("systemctl", "stop", "zerohourd.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop zerohourd.service: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	fmt.Println("Stopped systemd service: zerohourd.service")
	return nil
}

func isAnyNodeRunning(goos string) (bool, error) {
	for _, name := range nodeRuntimeProcessNames(goos) {
		running, err := isProcessRunning(goos, name)
		if err != nil {
			return false, err
		}
		if running {
			return true, nil
		}
	}
	return false, nil
}

func isProcessRunning(goos string, name string) (bool, error) {
	var cmd *exec.Cmd
	if goos == "windows" {
		cmd = exec.Command("tasklist", "/FI", "IMAGENAME eq "+name)
	} else {
		cmd = exec.Command("pgrep", "-x", name)
	}
	output, err := cmd.CombinedOutput()
	if goos == "windows" {
		if err != nil {
			return false, err
		}
		return strings.Contains(strings.ToLower(string(output)), strings.ToLower(name)), nil
	}
	if err == nil {
		return strings.TrimSpace(string(output)) != "", nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func terminateProcess(goos string, name string) error {
	var cmd *exec.Cmd
	if goos == "windows" {
		cmd = exec.Command("taskkill", "/IM", name, "/T", "/F")
	} else {
		cmd = exec.Command("pkill", "-TERM", "-x", name)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop %s: %w\n%s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func startNodeIfNotRunning(goos string, nodeDir string, dataDir string) error {
	fmt.Println()
	fmt.Println("==> START ZHCASH NODE")
	running, err := isAnyNodeRunning(goos)
	if err != nil {
		return err
	}
	if running {
		fmt.Println("ZHCASH node is already running; not starting another instance.")
		return nil
	}
	executable, err := findNodeExecutable(goos, nodeDir)
	if err != nil {
		if goos == "darwin" {
			fmt.Println("macOS node executable is not available yet; not starting node.")
			return nil
		}
		return err
	}
	cmd := exec.Command(executable, "-datadir="+dataDir)
	cmd.Dir = filepath.Dir(executable)
	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	fmt.Println("Started:", executable)
	return nil
}

func reportInstallerNodeFailure(dataDir string, startedAt time.Time, startupErr error, telemetryDisabled bool) error {
	elapsed := time.Since(startedAt)
	status := "failure"
	if errors.Is(startupErr, errNodeStartupTimeout) {
		status = "timeout"
	}
	payload := newInstallerTelemetryPayload(status, elapsed, nodeReadiness{}, startupErr, collectNodeDiagnostics(dataDir))
	diagnosticDir, diagnosticDirErr := installerDiagnosticDirectory()
	if diagnosticDirErr != nil {
		diagnosticDir = os.TempDir()
	}
	reportPath, reportErr := writeInstallerDiagnosticReport(diagnosticDir, payload)
	if reportErr != nil {
		fmt.Println("WARNING: could not save local installer diagnostics:", reportErr)
	} else {
		fmt.Println("Saved sanitized installer diagnostics:", reportPath)
	}
	if !telemetryDisabled {
		telemetryCtx, telemetryCancel := context.WithTimeout(context.Background(), 10*time.Second)
		telemetryErr := sendInstallerTelemetry(telemetryCtx, defaultInstallerTelemetryURL, payload)
		telemetryCancel()
		if telemetryErr != nil {
			fmt.Println("WARNING: installer diagnostics could not be delivered to wallet.zeroscan.st:", telemetryErr)
		} else {
			fmt.Println("Sanitized startup diagnostics were sent through wallet.zeroscan.st for administrator review.")
		}
	}
	return startupErr
}

func findNodeExecutable(goos string, nodeDir string) (string, error) {
	return findNamedExecutable(goos, nodeDir, "zerohour-qt")
}

func findNamedExecutable(goos string, nodeDir string, name string) (string, error) {
	if goos == "windows" {
		name += ".exe"
	}
	var found string
	err := filepath.WalkDir(nodeDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if goos == "windows" {
			if strings.EqualFold(base, name) {
				found = path
				return filepath.SkipAll
			}
			return nil
		}
		if base == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%s not found in %s", name, nodeDir)
	}
	return found, nil
}

func configureZerohourdSystemd(dataDir string, nodeDir string) error {
	fmt.Println()
	fmt.Println("==> CONFIGURE ZEROHOURD SYSTEMD SERVICE")
	if os.Geteuid() != 0 {
		return errors.New("Linux server mode requires root privileges to write /etc/systemd/system/zerohourd.service")
	}
	daemonPath, err := findNamedExecutable("linux", nodeDir, "zerohourd")
	if err != nil {
		return err
	}
	unit := zerohourdServiceUnit(daemonPath, dataDir)
	if err := os.WriteFile(zerohourdServicePath, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", "zerohourd.service"},
		{"restart", "zerohourd.service"},
	} {
		output, err := exec.Command("systemctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
	}
	fmt.Println("Systemd service enabled and restarted: zerohourd.service")
	return nil
}

func zerohourdServiceUnit(daemonPath string, dataDir string) string {
	return fmt.Sprintf(`[Unit]
Description=ZHCASH daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s -datadir=%s
Restart=always
RestartSec=10
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, daemonPath, dataDir)
}

func defaultNodeDir(goos string, env map[string]string, runDir string) (string, error) {
	if configured := strings.TrimSpace(env[nodeDirVariable]); configured != "" {
		return configured, nil
	}
	switch goos {
	case "windows":
		home := env["USERPROFILE"]
		if home == "" {
			home = env["HOME"]
		}
		if home == "" {
			return "", errors.New("USERPROFILE is not set")
		}
		return filepath.Join(home, "Desktop"), nil
	case "linux":
		if isLinuxServer(goos, env) {
			if env["HOME"] == "" {
				return "", errors.New("HOME is not set")
			}
			return filepath.Join(env["HOME"], "ZHCASH"), nil
		}
		return runDir, nil
	default:
		return runDir, nil
	}
}

func getenvMap() map[string]string {
	out := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

type environmentUpdate struct {
	Name  string
	Value string
}

func missingEnvironmentUpdates(env map[string]string, dataDir string, nodeDir string) []environmentUpdate {
	var updates []environmentUpdate
	if strings.TrimSpace(env[dataDirVariable]) == "" && dataDir != "" {
		updates = append(updates, environmentUpdate{Name: dataDirVariable, Value: dataDir})
	}
	if strings.TrimSpace(env[nodeDirVariable]) == "" && nodeDir != "" {
		updates = append(updates, environmentUpdate{Name: nodeDirVariable, Value: nodeDir})
	}
	return updates
}

func ensureEnvironmentVariables(goos string, env map[string]string, dataDir string, nodeDir string) error {
	updates := missingEnvironmentUpdates(env, dataDir, nodeDir)
	if len(updates) == 0 {
		return nil
	}
	fmt.Println("Configuring missing ZHCASH environment variables...")
	for _, update := range updates {
		if err := os.Setenv(update.Name, update.Value); err != nil {
			return err
		}
		if err := persistUserEnvironmentVariable(goos, update.Name, update.Value); err != nil {
			return err
		}
		fmt.Printf("%s=%s\n", update.Name, update.Value)
	}
	return nil
}

func persistUserEnvironmentVariable(goos string, name string, value string) error {
	if goos == "windows" {
		output, err := exec.Command("setx", name, value).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to persist %s with setx: %w\n%s", name, err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	envFile := filepath.Join(home, ".zhcash-env")
	if err := upsertShellExport(envFile, name, value); err != nil {
		return err
	}
	profile := filepath.Join(home, ".profile")
	if goos == "darwin" {
		profile = filepath.Join(home, ".zprofile")
	}
	return ensureProfileSourcesEnvFile(profile, envFile)
}

func upsertShellExport(path string, name string, value string) error {
	line := fmt.Sprintf("export %s=%s", name, shellSingleQuote(value))
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(content), "\n")
	replaced := false
	for i, existing := range lines {
		if strings.HasPrefix(existing, "export "+name+"=") {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		if len(lines) == 1 && lines[0] == "" {
			lines[0] = line
		} else {
			lines = append(lines, line)
		}
	}
	output := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	return os.WriteFile(path, []byte(output), 0o644)
}

func ensureProfileSourcesEnvFile(profile string, envFile string) error {
	sourceLine := fmt.Sprintf("[ -f %s ] && . %s", shellSingleQuote(envFile), shellSingleQuote(envFile))
	content, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(content), sourceLine) {
		return nil
	}
	output := strings.TrimRight(string(content), "\n")
	if output != "" {
		output += "\n"
	}
	output += sourceLine + "\n"
	return os.WriteFile(profile, []byte(output), 0o644)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func cleanBlockchainData(dataDir string) (int, error) {
	clean, err := filepath.Abs(dataDir)
	if err != nil {
		return 0, err
	}
	if isDangerousPath(clean) {
		return 0, fmt.Errorf("refusing to clean dangerous data directory: %s", dataDir)
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(clean)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if preserveDataEntry(entry.Name(), entry.IsDir()) {
			continue
		}
		fmt.Println("Removing old data:", entry.Name())
		if err := os.RemoveAll(filepath.Join(clean, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	remaining, err := os.ReadDir(clean)
	if err != nil {
		return removed, err
	}
	if err := printRemainingDataEntries(clean); err != nil {
		return removed, err
	}
	for _, entry := range remaining {
		if !preserveDataEntry(entry.Name(), entry.IsDir()) {
			return removed, fmt.Errorf("old blockchain data still exists after cleanup: %s", filepath.Join(clean, entry.Name()))
		}
	}
	return removed, nil
}

func printRemainingDataEntries(dataDir string) error {
	found := false
	fmt.Println("Files and directories remaining after cleanup:")
	err := filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dataDir {
			return nil
		}
		found = true
		relative, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		} else if entry.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		fmt.Printf("- %s: %q\n", kind, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		fmt.Println("- none")
	}
	return nil
}

type configurationBackup struct {
	name string
	data []byte
	mode os.FileMode
}

func captureConfigurationFiles(dataDir string) ([]configurationBackup, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	backups := make([]configurationBackup, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".conf") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			fmt.Printf("Configuration path preserved without in-memory backup: %q\n", entry.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(dataDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		backups = append(backups, configurationBackup{name: entry.Name(), data: data, mode: info.Mode()})
	}
	if len(backups) == 0 {
		fmt.Printf("WARNING: no regular *.conf file exists in %s; there is no local configuration to preserve.\n", dataDir)
		return backups, nil
	}
	fmt.Println("Configuration files backed up in memory before cleanup:")
	for _, backup := range backups {
		fmt.Printf("- %q\n", backup.name)
	}
	return backups, nil
}

func restoreConfigurationFiles(dataDir string, backups []configurationBackup) error {
	for _, backup := range backups {
		target := filepath.Join(dataDir, backup.name)
		current, err := os.ReadFile(target)
		if err == nil && bytes.Equal(current, backup.data) {
			fmt.Printf("Configuration verified unchanged: %q\n", backup.name)
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("verify configuration %s: %w", target, err)
		}
		if err := os.WriteFile(target, backup.data, backup.mode.Perm()); err != nil {
			return fmt.Errorf("restore configuration %s: %w", target, err)
		}
		fmt.Printf("Configuration restored from in-memory backup: %q\n", backup.name)
	}
	return nil
}

func preserveDataEntry(name string, isDir bool) bool {
	lower := strings.ToLower(name)
	if !isDir && (lower == strings.ToLower(defaultOutputName) || lower == strings.ToLower(defaultOutputName+".part")) {
		return true
	}
	if !isDir && (lower == "wallet.dat" || strings.HasSuffix(lower, ".bak") || strings.HasSuffix(lower, ".conf")) {
		return true
	}
	if isDir && (lower == "wallet" || lower == "wallets" || lower == "zhp2pproxy") {
		return true
	}
	return false
}

func removeSnapshotArchive(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	fmt.Println("Removed Snapshot archive:", path)
	return nil
}

func shouldRemoveSnapshotArchive(keep bool) bool {
	return !keep
}

func prepareExistingSnapshotArchive(path string, expectedSize int64, expectedSHA256 string) (bool, error) {
	partPath := snapshotPartPath(path)
	if err := os.Remove(partPath); err == nil {
		fmt.Println("Removed incomplete Snapshot partial:", partPath)
	} else if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stat.Size() != expectedSize {
		fmt.Printf("Existing Snapshot archive has wrong size (%s, expected %s); deleting it.\n", humanBytes(stat.Size()), humanBytes(expectedSize))
		if err := os.Remove(path); err != nil {
			return false, err
		}
		return false, nil
	}
	fmt.Println("Existing Snapshot archive found; verifying SHA256 before reuse.")
	if err := verifySHA256File(path, expectedSHA256); err != nil {
		fmt.Println("Existing Snapshot archive failed SHA256 verification; deleting it.")
		if removeErr := os.Remove(path); removeErr != nil {
			return false, removeErr
		}
		return false, nil
	}
	fmt.Println("Existing Snapshot archive SHA256 OK.")
	return true, nil
}

func isDangerousPath(path string) bool {
	volume := filepath.VolumeName(path)
	withoutVolume := strings.TrimPrefix(path, volume)
	if withoutVolume == string(filepath.Separator) || withoutVolume == "." || withoutVolume == "" {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return base == "" || base == "." || base == string(filepath.Separator)
}

func extractZipArchive(zipPath string, destination string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, item := range reader.File {
		if preserveSnapshotTarget(item.Name) {
			continue
		}
		target, err := safeJoin(destination, item.Name)
		if err != nil {
			return err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := item.Open()
		if err != nil {
			return err
		}
		if err := writeFileFromReader(target, src, item.FileInfo().Mode()); err != nil {
			_ = src.Close()
			return err
		}
		if err := src.Close(); err != nil {
			return err
		}
	}
	return nil
}

// preserveSnapshotTarget prevents a Snapshot from overwriting local wallets,
// wallet backups, node configuration, or the local zhp2pproxy installation.
// These entries are never blockchain data and must always remain owned by the
// local installation.
func preserveSnapshotTarget(name string) bool {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == "/" {
		return false
	}
	clean = strings.TrimPrefix(clean, "/")
	top := strings.ToLower(strings.Split(clean, "/")[0])
	return top == "wallet.dat" || top == "wallet" || top == "wallets" || top == "zhp2pproxy" ||
		strings.HasSuffix(top, ".bak") || strings.HasSuffix(top, ".conf")
}

func extractSingleFileFromZip(zipPath string, filename string, destination string) (string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	for _, item := range reader.File {
		if item.FileInfo().IsDir() || !strings.EqualFold(filepath.Base(item.Name), filename) {
			continue
		}
		target, err := safeJoin(destination, filename)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		src, err := item.Open()
		if err != nil {
			return "", err
		}
		mode := item.FileInfo().Mode()
		if mode == 0 {
			mode = 0o755
		}
		if err := writeFileFromReader(target, src, mode|0o755); err != nil {
			_ = src.Close()
			return "", err
		}
		if err := src.Close(); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("%s not found in %s", filename, zipPath)
}

func extractTarGzArchive(archivePath string, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(destination, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFileFromReader(target, tr, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
}

func safeJoin(root string, child string) (string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(cleanRoot, child))
	if err != nil {
		return "", err
	}
	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", child)
	}
	return target, nil
}

func writeFileFromReader(path string, reader io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, reader)
	return err
}

func configuredYandexURL() string {
	if decoded, err := decodeObfuscatedYandexURL(yandexURLPayload, yandexURLKey); err == nil && decoded != "" {
		return decoded
	}
	return os.Getenv(yandexURLVariable)
}

func decodeObfuscatedYandexURL(payload string, key string) (string, error) {
	if payload == "" || key == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", err
	}
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	if len(keyBytes) == 0 {
		return "", errors.New("empty obfuscation key")
	}
	for i := range data {
		data[i] ^= keyBytes[i%len(keyBytes)]
	}
	return string(data), nil
}

func downloadFromSource(ctx context.Context, source sourceConfig, outputPath string, partPath string, idleTimeout time.Duration) error {
	switch source.Kind {
	case sourceMega:
		return downloadMega(ctx, source.URL, outputPath, partPath, idleTimeout)
	case sourceYandex:
		if source.URL == "" {
			return fmt.Errorf("%s is not set", yandexURLVariable)
		}
		info, err := requestYandexFileInfo(ctx, source.URL)
		if err != nil {
			return err
		}
		return downloadPlainHTTP(ctx, info.DownloadURL, outputPath, partPath, info.Size, idleTimeout)
	case sourceGithub:
		return downloadMultipartHTTP(ctx, githubSnapshotPartURLs(), outputPath, partPath, idleTimeout)
	case sourceZeroscan:
		size, err := requestHTTPContentLength(ctx, source.URL)
		if err != nil {
			return err
		}
		return downloadPlainHTTP(ctx, source.URL, outputPath, partPath, size, idleTimeout)
	default:
		return fmt.Errorf("unsupported source kind: %s", source.Kind)
	}
}

func githubSnapshotPartURLs() []string {
	urls := make([]string, defaultGithubPartCount)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s/%s.part%02d", defaultGithubReleaseBaseURL, defaultOutputName, i+1)
	}
	return urls
}

func downloadSnapshot(ctx context.Context, sources []sourceConfig, dataDir string, force bool, idleTimeout time.Duration, sourceRetries int) (string, error) {
	outputPath := filepath.Join(dataDir, defaultOutputName)
	partPath := snapshotPartPath(outputPath)
	if force {
		_ = os.Remove(outputPath)
		_ = os.Remove(partPath)
	}
	var failures []string
	attempts := sourceRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	for _, source := range sources {
		fmt.Println()
		fmt.Println("==> DOWNLOAD SNAPSHOT FROM", strings.ToUpper(source.Name))
		for attempt := 1; attempt <= attempts; attempt++ {
			if attempt > 1 {
				fmt.Printf("Retrying source %s: attempt %d/%d\n", source.Name, attempt, attempts)
			}
			if err := downloadFromSource(ctx, source, outputPath, partPath, idleTimeout); err != nil {
				failures = append(failures, fmt.Sprintf("%s attempt %d/%d: %v", source.Name, attempt, attempts, err))
				fmt.Printf("source failed: %s attempt %d/%d: %v\n", source.Name, attempt, attempts, err)
				continue
			}
			if err := verifySHA256File(outputPath, snapshotSHA256); err != nil {
				_ = os.Remove(outputPath)
				failures = append(failures, fmt.Sprintf("%s checksum: %v", source.Name, err))
				fmt.Printf("source failed: %s checksum: %v\n", source.Name, err)
				continue
			}
			fmt.Println("Snapshot SHA256 OK")
			return outputPath, nil
		}
		fmt.Println("switching to next source")
	}
	return "", fmt.Errorf("all snapshot sources failed: %s", strings.Join(failures, "; "))
}

func snapshotPartPath(outputPath string) string {
	return outputPath + ".part"
}

func verifySnapshotLayout(dataDir string) error {
	for _, required := range []string{"blocks", "chainstate"} {
		info, err := os.Stat(filepath.Join(dataDir, required))
		if err != nil {
			return fmt.Errorf("snapshot missing %s: %w", required, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("snapshot path is not a directory: %s", required)
		}
	}
	return nil
}

type nodeReleaseAsset struct {
	Filename string
	SHA256   string
}

var ubuntuConsoleNodeReleases = map[string]nodeReleaseAsset{
	"20.04": {Filename: "zhcash-evolution-1.0.0-linux-x86_64-ubuntu20.04-console.tar.gz", SHA256: "624ea08419afc928a6ab6d8b25a717af1656b77e24300d762dfaaa7ff490a4b4"},
	"22.04": {Filename: "zhcash-evolution-1.0.0-linux-x86_64-ubuntu22.04-console.tar.gz", SHA256: "228d55481db7d3f40697a19e550abd02674503ad9b7da1c3165c7e0d8400b283"},
	"24.04": {Filename: "zhcash-evolution-1.0.0-linux-x86_64-ubuntu24.04-console.tar.gz", SHA256: "8cd8622cce9bdfbd2f177b707bf191e9f3b2ba58c4444b96395722a0fa52ba00"},
}

func linuxNodeRelease(serverMode bool, osReleaseContent string) (nodeReleaseAsset, string) {
	fallback := nodeReleaseAsset{Filename: linuxNodeArchive, SHA256: linuxNodeSHA256}
	if !serverMode {
		return fallback, "Linux GUI-compatible release"
	}
	values := parseOSRelease(osReleaseContent)
	if strings.EqualFold(values["ID"], "ubuntu") {
		if selected, ok := ubuntuConsoleNodeReleases[values["VERSION_ID"]]; ok {
			return selected, "Ubuntu " + values["VERSION_ID"] + " console/server release"
		}
	}
	return fallback, "compatible Linux fallback release"
}

func parseOSRelease(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	return values
}

func readOSRelease() string {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return string(content)
}

func verifiedNodeReleaseDownload(ctx context.Context, asset nodeReleaseAsset, destination string, idleTimeout time.Duration) error {
	sourceURL := nodeReleaseBaseURL + "/" + asset.Filename
	if err := downloadPlainHTTPFile(ctx, sourceURL, destination, destination+".part", idleTimeout); err != nil {
		return err
	}
	if err := verifySHA256File(destination, asset.SHA256); err != nil {
		return fmt.Errorf("verify official node release %s: %w", asset.Filename, err)
	}
	fmt.Println("Node release SHA256 verified:", asset.SHA256)
	return nil
}

func installNodeRelease(ctx context.Context, goos string, nodeDir string, idleTimeout time.Duration, linuxServer bool) error {
	fmt.Println()
	fmt.Println("==> INSTALL NODE RELEASE")
	switch goos {
	case "windows":
		tempDir, err := os.MkdirTemp("", "zhcash-node-release-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tempDir)
		asset := nodeReleaseAsset{Filename: windowsNodeArchive, SHA256: windowsNodeSHA256}
		archive := filepath.Join(tempDir, asset.Filename)
		if err := verifiedNodeReleaseDownload(ctx, asset, archive, idleTimeout); err != nil {
			return err
		}
		target, err := extractSingleFileFromZip(archive, "zerohour-qt.exe", nodeDir)
		if err != nil {
			return err
		}
		fmt.Println("Windows node installed:", target)
		return nil
	case "linux":
		asset, description := linuxNodeRelease(linuxServer, readOSRelease())
		fmt.Printf("Selected %s: %s\n", description, asset.Filename)
		archive := filepath.Join(nodeDir, asset.Filename)
		if err := verifiedNodeReleaseDownload(ctx, asset, archive, idleTimeout); err != nil {
			return err
		}
		if err := extractTarGzArchive(archive, nodeDir); err != nil {
			return err
		}
		fmt.Println("Linux node release extracted to:", nodeDir)
		return nil
	case "darwin":
		fmt.Println("macOS node package is not available in ZHCASH v1.0.0 yet. Snapshot installation completed; macOS node release will be added later.")
		return nil
	default:
		return fmt.Errorf("unsupported OS for node release: %s", goos)
	}
}

func downloadMega(ctx context.Context, megaLink string, outputPath string, partPath string, idleTimeout time.Duration) error {
	fileID, fileKey, err := parseMegaLink(megaLink)
	if err != nil {
		return err
	}
	params, err := deriveMegaCryptoParams(fileKey)
	if err != nil {
		return err
	}
	fmt.Println("Mega file id:", fileID)
	info, err := requestMegaFileInfo(ctx, fileID)
	if err != nil {
		return err
	}
	fmt.Println("Size:", humanBytes(info.Size))
	if complete, err := existingComplete(outputPath, info.Size); err != nil {
		return err
	} else if complete {
		fmt.Println("File already exists and has expected size.")
		return verifyZipHeader(outputPath)
	}
	offset, err := resumeOffset(partPath, info.Size)
	if err != nil {
		return err
	}
	if done, err := finalizeCompletePart(outputPath, partPath, info.Size, verifyZipHeader); err != nil {
		return err
	} else if done {
		return nil
	}
	if err := downloadAndDecrypt(ctx, info.DownloadURL, params, partPath, offset, info.Size, idleTimeout); err != nil {
		return err
	}
	return finalizeDownloadedPart(outputPath, partPath)
}

func finalizeDownloadedPart(outputPath string, partPath string) error {
	if err := verifyZipHeader(partPath); err != nil {
		return err
	}
	return renameDownloadedPart(outputPath, partPath)
}

func finalizeCompletePart(outputPath string, partPath string, expectedSize int64, verify func(string) error) (bool, error) {
	stat, err := os.Stat(partPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stat.Size() < expectedSize {
		return false, nil
	}
	if stat.Size() > expectedSize {
		return false, nil
	}
	fmt.Println("Partial file already has expected size; finalizing it.")
	if verify != nil {
		if err := verify(partPath); err != nil {
			return false, err
		}
	}
	if err := renameDownloadedPart(outputPath, partPath); err != nil {
		return false, err
	}
	return true, nil
}

func renameDownloadedPart(outputPath string, partPath string) error {
	if err := os.Rename(partPath, outputPath); err != nil {
		return err
	}
	fmt.Println("Done:", outputPath)
	return nil
}

func parseMegaLink(link string) (string, string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", "", err
	}
	if u.Fragment == "" {
		return "", "", errors.New("Mega URL must contain decryption key after #")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "file" && parts[i+1] != "" {
			return parts[i+1], u.Fragment, nil
		}
	}
	return "", "", fmt.Errorf("unsupported Mega file URL path: %s", u.Path)
}

func deriveMegaCryptoParams(fileKey string) (megaCryptoParams, error) {
	raw, err := base64.RawURLEncoding.DecodeString(fileKey)
	if err != nil {
		return megaCryptoParams{}, err
	}
	if len(raw) != 32 {
		return megaCryptoParams{}, fmt.Errorf("unsupported Mega key length: %d bytes", len(raw))
	}
	words := make([]uint32, 8)
	for i := range words {
		words[i] = binary.BigEndian.Uint32(raw[i*4 : i*4+4])
	}
	key := make([]byte, 16)
	binary.BigEndian.PutUint32(key[0:4], words[0]^words[4])
	binary.BigEndian.PutUint32(key[4:8], words[1]^words[5])
	binary.BigEndian.PutUint32(key[8:12], words[2]^words[6])
	binary.BigEndian.PutUint32(key[12:16], words[3]^words[7])

	iv := make([]byte, 16)
	binary.BigEndian.PutUint32(iv[0:4], words[4])
	binary.BigEndian.PutUint32(iv[4:8], words[5])
	return megaCryptoParams{Key: key, IV: iv}, nil
}

func requestMegaFileInfo(ctx context.Context, fileID string) (megaFileInfo, error) {
	payload := []map[string]any{{"a": "g", "g": 1, "p": fileID}}
	body, err := json.Marshal(payload)
	if err != nil {
		return megaFileInfo{}, err
	}
	endpoint := fmt.Sprintf("https://g.api.mega.co.nz/cs?id=%d", time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return megaFileInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return megaFileInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return megaFileInfo{}, fmt.Errorf("Mega API HTTP status: %s", resp.Status)
	}

	var decoded []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return megaFileInfo{}, err
	}
	if len(decoded) == 0 {
		return megaFileInfo{}, errors.New("empty Mega API response")
	}
	var code int
	if err := json.Unmarshal(decoded[0], &code); err == nil && code < 0 {
		return megaFileInfo{}, fmt.Errorf("Mega API error code: %d", code)
	}
	var item struct {
		Size int64  `json:"s"`
		URL  string `json:"g"`
	}
	if err := json.Unmarshal(decoded[0], &item); err != nil {
		return megaFileInfo{}, err
	}
	if item.Size <= 0 || item.URL == "" {
		return megaFileInfo{}, fmt.Errorf("unexpected Mega API response: %s", string(decoded[0]))
	}
	return megaFileInfo{Size: item.Size, DownloadURL: item.URL}, nil
}

func requestYandexFileInfo(ctx context.Context, publicURL string) (megaFileInfo, error) {
	endpoint := "https://cloud-api.yandex.net/v1/disk/public/resources/download?public_key=" + url.QueryEscape(publicURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return megaFileInfo{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return megaFileInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return megaFileInfo{}, fmt.Errorf("Yandex API HTTP status: %s", resp.Status)
	}
	var item struct {
		URL string `json:"href"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return megaFileInfo{}, err
	}
	if item.URL == "" {
		return megaFileInfo{}, errors.New("Yandex API response has no href")
	}
	size, err := requestHTTPContentLength(ctx, item.URL)
	if err != nil {
		return megaFileInfo{}, err
	}
	return megaFileInfo{Size: size, DownloadURL: item.URL}, nil
}

func requestHTTPContentLength(ctx context.Context, sourceURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, sourceURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP HEAD status: %s", resp.Status)
	}
	if resp.ContentLength <= 0 {
		return 0, errors.New("HTTP source did not provide Content-Length")
	}
	return resp.ContentLength, nil
}

func existingComplete(path string, size int64) (bool, error) {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stat.Size() > size {
		return false, fmt.Errorf("existing file is larger than expected: %s > %s; use --force", humanBytes(stat.Size()), humanBytes(size))
	}
	return stat.Size() == size, nil
}

func verifySHA256File(path string, expectedHex string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, expectedHex) {
		return fmt.Errorf("SHA256 mismatch for %s: got %s expected %s", path, got, expectedHex)
	}
	return nil
}

func resumeOffset(partPath string, targetSize int64) (int64, error) {
	offset := int64(0)
	if stat, err := os.Stat(partPath); err == nil {
		offset = stat.Size()
		if offset > targetSize {
			fmt.Printf("Partial file is larger than expected (%s > %s); deleting and downloading from zero.\n", humanBytes(offset), humanBytes(targetSize))
			if err := os.Remove(partPath); err != nil {
				return 0, err
			}
			return 0, nil
		}
	}
	if offset > 0 {
		fmt.Printf("Resuming from %s\n", humanBytes(offset))
	}
	return offset, nil
}

func downloadPlainHTTP(ctx context.Context, sourceURL string, outputPath string, partPath string, size int64, idleTimeout time.Duration) error {
	if err := downloadPlainHTTPFileWithSize(ctx, sourceURL, outputPath, partPath, size, idleTimeout); err != nil {
		return err
	}
	return verifyZipHeader(outputPath)
}

func downloadPlainHTTPFile(ctx context.Context, sourceURL string, outputPath string, partPath string, idleTimeout time.Duration) error {
	size, err := requestHTTPContentLength(ctx, sourceURL)
	if err != nil {
		return err
	}
	return downloadPlainHTTPFileWithSize(ctx, sourceURL, outputPath, partPath, size, idleTimeout)
}

func downloadPlainHTTPFileWithSize(ctx context.Context, sourceURL string, outputPath string, partPath string, size int64, idleTimeout time.Duration) error {
	fmt.Println("Size:", humanBytes(size))
	if complete, err := existingComplete(outputPath, size); err != nil {
		return err
	} else if complete {
		fmt.Println("File already exists and has expected size.")
		return nil
	}
	offset, err := resumeOffset(partPath, size)
	if err != nil {
		return err
	}
	if done, err := finalizeCompletePart(outputPath, partPath, size, nil); err != nil {
		return err
	} else if done {
		return nil
	}

	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	closed := false
	closeOut := func() error {
		if closed {
			return nil
		}
		closed = true
		return out.Close()
	}
	defer closeOut()
	if _, err := out.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	downloadCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := newIdleWatchdog(idleTimeout, cancel)
	defer watchdog.stop()

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, size-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download HTTP status: %s", resp.Status)
	}
	if offset > 0 && resp.StatusCode != http.StatusPartialContent {
		return errors.New("server ignored Range resume request")
	}

	progress := &progressReader{
		reader:     resp.Body,
		start:      time.Now(),
		last:       time.Now(),
		offset:     offset,
		total:      size,
		onProgress: watchdog.progress,
	}
	if _, err := io.Copy(out, progress); err != nil {
		if errors.Is(context.Cause(downloadCtx), errIdleTimeout) {
			return fmt.Errorf("%w after %s without progress", errIdleTimeout, idleTimeout)
		}
		return err
	}
	fmt.Println()
	if err := closeOut(); err != nil {
		return err
	}

	stat, err := os.Stat(partPath)
	if err != nil {
		return err
	}
	if stat.Size() != size {
		return fmt.Errorf("downloaded size mismatch: got %s, expected %s; re-run to resume", humanBytes(stat.Size()), humanBytes(size))
	}
	return renameDownloadedPart(outputPath, partPath)
}

func downloadMultipartHTTP(ctx context.Context, partURLs []string, outputPath string, partPath string, idleTimeout time.Duration) error {
	if len(partURLs) == 0 {
		return errors.New("multipart source has no parts")
	}
	partSizes := make([]int64, len(partURLs))
	totalSize := int64(0)
	for i, partURL := range partURLs {
		size, err := requestHTTPContentLength(ctx, partURL)
		if err != nil {
			return fmt.Errorf("part %02d size: %w", i+1, err)
		}
		partSizes[i] = size
		totalSize += size
	}
	fmt.Printf("Parts: %d\n", len(partURLs))
	fmt.Println("Size:", humanBytes(totalSize))
	if complete, err := existingComplete(outputPath, totalSize); err != nil {
		return err
	} else if complete {
		fmt.Println("File already exists and has expected size.")
		return verifyZipHeader(outputPath)
	}
	offset, err := resumeOffset(partPath, totalSize)
	if err != nil {
		return err
	}
	if done, err := finalizeCompletePart(outputPath, partPath, totalSize, verifyZipHeader); err != nil {
		return err
	} else if done {
		return nil
	}

	partStart := int64(0)
	for i, partURL := range partURLs {
		partSize := partSizes[i]
		partEnd := partStart + partSize
		if offset >= partEnd {
			partStart = partEnd
			continue
		}
		partOffset := int64(0)
		if offset > partStart {
			partOffset = offset - partStart
		}
		fmt.Printf("GitHub part %02d/%02d\n", i+1, len(partURLs))
		if err := appendHTTPRangeToFile(ctx, partURL, partPath, partOffset, partSize, partStart, totalSize, idleTimeout); err != nil {
			return fmt.Errorf("part %02d: %w", i+1, err)
		}
		offset = partEnd
		partStart = partEnd
	}

	stat, err := os.Stat(partPath)
	if err != nil {
		return err
	}
	if stat.Size() != totalSize {
		return fmt.Errorf("multipart downloaded size mismatch: got %s, expected %s; re-run to resume", humanBytes(stat.Size()), humanBytes(totalSize))
	}
	if err := verifyZipHeader(partPath); err != nil {
		return err
	}
	return renameDownloadedPart(outputPath, partPath)
}

func appendHTTPRangeToFile(ctx context.Context, sourceURL string, outputPath string, offset int64, size int64, globalOffset int64, totalSize int64, idleTimeout time.Duration) error {
	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Seek(globalOffset+offset, io.SeekStart); err != nil {
		return err
	}

	downloadCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := newIdleWatchdog(idleTimeout, cancel)
	defer watchdog.stop()

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, size-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download HTTP status: %s", resp.Status)
	}
	if offset > 0 && resp.StatusCode != http.StatusPartialContent {
		return errors.New("server ignored Range resume request")
	}

	progress := &progressReader{
		reader:     resp.Body,
		start:      time.Now(),
		last:       time.Now(),
		offset:     globalOffset + offset,
		total:      totalSize,
		onProgress: watchdog.progress,
	}
	written, err := io.Copy(out, progress)
	if err != nil {
		if errors.Is(context.Cause(downloadCtx), errIdleTimeout) {
			return fmt.Errorf("%w after %s without progress", errIdleTimeout, idleTimeout)
		}
		return err
	}
	if written != size-offset {
		return fmt.Errorf("part size mismatch: got %s, expected %s", humanBytes(written), humanBytes(size-offset))
	}
	fmt.Println()
	return nil
}

func downloadAndDecrypt(ctx context.Context, sourceURL string, params megaCryptoParams, outputPath string, offset int64, targetSize int64, idleTimeout time.Duration) error {
	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	downloadCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := newIdleWatchdog(idleTimeout, cancel)
	defer watchdog.stop()

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	if offset > 0 || targetSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, targetSize-1))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download HTTP status: %s", resp.Status)
	}

	block, err := aes.NewCipher(params.Key)
	if err != nil {
		return err
	}
	stream := newCTRAtOffset(block, params.IV, offset)
	reader := &cipher.StreamReader{S: stream, R: resp.Body}
	progress := &progressReader{
		reader:     reader,
		start:      time.Now(),
		last:       time.Now(),
		offset:     offset,
		total:      targetSize,
		onProgress: watchdog.progress,
	}

	if _, err := io.Copy(out, progress); err != nil {
		if errors.Is(context.Cause(downloadCtx), errIdleTimeout) {
			return fmt.Errorf("%w after %s without progress", errIdleTimeout, idleTimeout)
		}
		return err
	}
	fmt.Println()

	stat, err := os.Stat(outputPath)
	if err != nil {
		return err
	}
	if stat.Size() != targetSize {
		return fmt.Errorf("downloaded size mismatch: got %s, expected %s; re-run to resume", humanBytes(stat.Size()), humanBytes(targetSize))
	}
	return nil
}

func newCTRAtOffset(block cipher.Block, iv []byte, offset int64) cipher.Stream {
	ctrIV := append([]byte(nil), iv...)
	blocks := uint64(offset / int64(block.BlockSize()))
	for i := len(ctrIV) - 1; blocks > 0 && i >= 0; i-- {
		sum := uint64(ctrIV[i]) + (blocks & 0xff)
		ctrIV[i] = byte(sum)
		blocks = (blocks >> 8) + (sum >> 8)
	}
	stream := cipher.NewCTR(block, ctrIV)
	if skip := int(offset % int64(block.BlockSize())); skip > 0 {
		dummy := make([]byte, skip)
		stream.XORKeyStream(dummy, dummy)
	}
	return stream
}

func verifyZipHeader(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(file, head); err != nil {
		return err
	}
	if !bytes.Equal(head, []byte{'P', 'K', 0x03, 0x04}) {
		return fmt.Errorf("not a ZIP file: header %x", head)
	}
	fmt.Println("ZIP header OK")
	return nil
}

type progressReader struct {
	reader     io.Reader
	start      time.Time
	last       time.Time
	offset     int64
	total      int64
	read       int64
	onProgress func()
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	if n > 0 {
		if p.onProgress != nil {
			p.onProgress()
		}
		p.read += int64(n)
		now := time.Now()
		if now.Sub(p.last) >= time.Second {
			p.print(now)
			p.last = now
		}
	}
	if errors.Is(err, io.EOF) {
		p.print(time.Now())
	}
	return n, err
}

type idleWatchdog struct {
	timeout time.Duration
	timer   *time.Timer
	mu      sync.Mutex
	stopped bool
}

func newIdleWatchdog(timeout time.Duration, cancel context.CancelCauseFunc) *idleWatchdog {
	w := &idleWatchdog{timeout: timeout}
	if timeout <= 0 {
		return w
	}
	w.timer = time.AfterFunc(timeout, func() {
		cancel(errIdleTimeout)
	})
	return w
}

func (w *idleWatchdog) progress() {
	if w.timeout <= 0 || w.timer == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.timer.Reset(w.timeout)
}

func (w *idleWatchdog) stop() {
	if w.timeout <= 0 || w.timer == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	w.timer.Stop()
}

func (p *progressReader) print(now time.Time) {
	done := p.offset + p.read
	elapsed := now.Sub(p.start).Seconds()
	speed := float64(p.read)
	if elapsed > 0 {
		speed /= elapsed
	}
	percent := int64(0)
	if p.total > 0 {
		percent = done * 100 / p.total
	}
	eta := "--"
	if speed > 0 && p.total > done {
		eta = (time.Duration(float64(p.total-done)/speed) * time.Second).Round(time.Second).String()
	}
	fmt.Printf("\rDownloaded: %s / %s | %d%% | %s/s | ETA %s",
		humanBytes(done),
		humanBytes(p.total),
		percent,
		humanBytes(int64(speed)),
		eta,
	)
}

func humanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
