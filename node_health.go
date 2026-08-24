package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultInstallerTelemetryURL = "https://wallet.zeroscan.st/feedback"
const defaultNodeStartupTimeout = 10 * time.Minute
const defaultNodeStartupPollInterval = 5 * time.Second
const installerVersion = "0.2.13"
const maxNodeDiagnosticTailBytes int64 = 24 * 1024

var errNodeStartupTimeout = errors.New("node startup timeout")

type localNodeRPCSettings struct {
	URL      string
	Username string
	Password string
}

type nodeReadiness struct {
	BlockHeight   int64  `json:"blockHeight"`
	BestBlockHash string `json:"bestBlockHash"`
	Connections   int64  `json:"connections"`
	RPCMethod     string `json:"rpcMethod"`
}

type nodeDiagnosticFile struct {
	Name          string `json:"name"`
	OriginalBytes int64  `json:"originalBytes"`
	Tail          string `json:"tail"`
}

type installerTelemetryClient struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

type installerTelemetryDiagnostics struct {
	Reason        string               `json:"reason"`
	Status        string               `json:"status"`
	ElapsedMS     int64                `json:"elapsedMs"`
	BlockHeight   int64                `json:"blockHeight,omitempty"`
	BestBlockHash string               `json:"bestBlockHash,omitempty"`
	Connections   int64                `json:"connections,omitempty"`
	RPCMethod     string               `json:"rpcMethod,omitempty"`
	Error         string               `json:"error,omitempty"`
	Logs          []nodeDiagnosticFile `json:"logs,omitempty"`
}

type installerTelemetryPayload struct {
	Version     int                           `json:"version"`
	Message     string                        `json:"message"`
	Client      installerTelemetryClient      `json:"client"`
	Diagnostics installerTelemetryDiagnostics `json:"diagnostics"`
}

type jsonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var diagnosticSecretPattern = regexp.MustCompile(`(?i)(rpcpassword|rpcuser|password|passwd|token|secret|private[_-]?key|wif)(\s*[=:]\s*)([^\s]+)`)

func readLocalNodeRPCSettings(dataDir string) (localNodeRPCSettings, error) {
	configPath := filepath.Join(dataDir, "zerohour.conf")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return localNodeRPCSettings{}, fmt.Errorf("read local RPC configuration: %w", err)
	}
	port := 3889
	if value, ok := activeMainConfigValue(string(content), "rpcport"); ok {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil || parsed < 1 || parsed > 65535 {
			return localNodeRPCSettings{}, fmt.Errorf("invalid rpcport in %s", configPath)
		}
		port = parsed
	}
	username, _ := activeMainConfigValue(string(content), "rpcuser")
	password, _ := activeMainConfigValue(string(content), "rpcpassword")
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return localNodeRPCSettings{}, errors.New("local RPC credentials are missing after node configuration")
	}
	return localNodeRPCSettings{
		URL:      fmt.Sprintf("http://127.0.0.1:%d/", port),
		Username: username,
		Password: password,
	}, nil
}

func callLocalNodeRPC(ctx context.Context, client *http.Client, settings localNodeRPCSettings, method string) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "1.0",
		"id":      "zhc-installer-readiness",
		"method":  method,
		"params":  []any{},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ZHC-Installer/"+installerVersion)
	req.SetBasicAuth(settings.Username, settings.Password)
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	var decoded jsonRPCResponse
	if err := json.Unmarshal(limited, &decoded); err != nil {
		return nil, fmt.Errorf("%s returned HTTP %d with invalid JSON", method, response.StatusCode)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("%s RPC error %d: %s", method, decoded.Error.Code, decoded.Error.Message)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned HTTP %d", method, response.StatusCode)
	}
	return decoded.Result, nil
}

func probeLocalNodeRPC(ctx context.Context, client *http.Client, settings localNodeRPCSettings) (nodeReadiness, error) {
	heightResult, err := callLocalNodeRPC(ctx, client, settings, "getblockcount")
	if err != nil {
		return nodeReadiness{}, err
	}
	var height int64
	if err := json.Unmarshal(heightResult, &height); err != nil || height < 0 {
		return nodeReadiness{}, errors.New("getblockcount returned an invalid height")
	}
	bestResult, err := callLocalNodeRPC(ctx, client, settings, "getbestblockhash")
	if err != nil {
		return nodeReadiness{}, err
	}
	var best string
	if err := json.Unmarshal(bestResult, &best); err != nil || len(strings.TrimSpace(best)) != 64 {
		return nodeReadiness{}, errors.New("getbestblockhash returned an invalid block hash")
	}
	connections := int64(0)
	if connectionResult, connectionErr := callLocalNodeRPC(ctx, client, settings, "getconnectioncount"); connectionErr == nil {
		_ = json.Unmarshal(connectionResult, &connections)
	}
	return nodeReadiness{
		BlockHeight:   height,
		BestBlockHash: best,
		Connections:   connections,
		RPCMethod:     "getblockcount+getbestblockhash",
	}, nil
}

func waitForLocalNodeReady(ctx context.Context, dataDir string, timeout time.Duration, pollInterval time.Duration, output io.Writer) (nodeReadiness, error) {
	if timeout <= 0 {
		timeout = defaultNodeStartupTimeout
	}
	if pollInterval <= 0 {
		pollInterval = defaultNodeStartupPollInterval
	}
	settings, err := readLocalNodeRPCSettings(dataDir)
	if err != nil {
		return nodeReadiness{}, err
	}
	startedAt := time.Now()
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: minDuration(4*time.Second, timeout)}
	lastPrintedAt := time.Time{}
	var lastErr error
	for {
		probeCtx, probeCancel := context.WithTimeout(deadlineCtx, minDuration(4*time.Second, timeout))
		ready, probeErr := probeLocalNodeRPC(probeCtx, client, settings)
		probeCancel()
		if probeErr == nil {
			return ready, nil
		}
		lastErr = probeErr
		now := time.Now()
		if lastPrintedAt.IsZero() || now.Sub(lastPrintedAt) >= 15*time.Second {
			fmt.Fprintf(output, "Waiting for local ZHC RPC: elapsed %s; last status: %s\n", now.Sub(startedAt).Round(time.Second), sanitizeDiagnosticText(probeErr.Error()))
			lastPrintedAt = now
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()
			return nodeReadiness{}, fmt.Errorf("%w: local ZHC RPC did not become ready within %s: %v", errNodeStartupTimeout, timeout, lastErr)
		case <-timer.C:
		}
	}
}

func minDuration(left time.Duration, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func sanitizeDiagnosticText(value string) string {
	text := strings.ToValidUTF8(value, "?")
	text = strings.ReplaceAll(text, "\x00", "")
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		text = strings.ReplaceAll(text, home, "<USER_HOME>")
		text = strings.ReplaceAll(text, filepath.ToSlash(home), "<USER_HOME>")
	}
	text = diagnosticSecretPattern.ReplaceAllString(text, "$1$2<redacted>")
	return text
}

func readDiagnosticTail(path string, maxBytes int64) (nodeDiagnosticFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nodeDiagnosticFile{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nodeDiagnosticFile{}, err
	}
	start := stat.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nodeDiagnosticFile{}, err
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return nodeDiagnosticFile{}, err
	}
	tail := sanitizeDiagnosticText(string(content))
	if int64(len(tail)) > maxBytes {
		tail = strings.ToValidUTF8(tail[len(tail)-int(maxBytes):], "?")
	}
	return nodeDiagnosticFile{
		Name:          filepath.Base(path),
		OriginalBytes: stat.Size(),
		Tail:          tail,
	}, nil
}

func collectNodeDiagnostics(dataDir string) []nodeDiagnosticFile {
	logs := make([]nodeDiagnosticFile, 0, 2)
	for _, name := range []string{"debug.log", "vm.log"} {
		entry, err := readDiagnosticTail(filepath.Join(dataDir, name), maxNodeDiagnosticTailBytes)
		if err == nil && strings.TrimSpace(entry.Tail) != "" {
			logs = append(logs, entry)
		}
	}
	return logs
}

func newInstallerTelemetryPayload(status string, elapsed time.Duration, readiness nodeReadiness, startupErr error, logs []nodeDiagnosticFile) installerTelemetryPayload {
	reason := "zhc_installer_success"
	message := "zhc_installer: node installation completed and local RPC is ready"
	if status == "timeout" {
		reason = "zhc_installer_timeout"
		message = "zhc_installer: node did not become ready within the startup timeout"
	} else if status != "success" {
		reason = "zhc_installer_failure"
		message = "zhc_installer: node could not be started"
	}
	errorText := ""
	if startupErr != nil {
		errorText = sanitizeDiagnosticText(startupErr.Error())
	}
	return installerTelemetryPayload{
		Version: 1,
		Message: message,
		Client: installerTelemetryClient{
			Kind:    "zhc-installer",
			Version: installerVersion,
			OS:      runtimeGOOS(),
			Arch:    runtimeGOARCH(),
		},
		Diagnostics: installerTelemetryDiagnostics{
			Reason:        reason,
			Status:        status,
			ElapsedMS:     elapsed.Milliseconds(),
			BlockHeight:   readiness.BlockHeight,
			BestBlockHash: readiness.BestBlockHash,
			Connections:   readiness.Connections,
			RPCMethod:     readiness.RPCMethod,
			Error:         errorText,
			Logs:          logs,
		},
	}
}

var runtimeGOOS = func() string { return runtime.GOOS }
var runtimeGOARCH = func() string { return runtime.GOARCH }

func sendInstallerTelemetry(ctx context.Context, endpoint string, payload installerTelemetryPayload) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "wallet.zeroscan.st" || parsed.Path != "/feedback" {
		return errors.New("installer telemetry endpoint must be https://wallet.zeroscan.st/feedback")
	}
	return postInstallerTelemetry(ctx, endpoint, payload, &http.Client{Timeout: 8 * time.Second})
}

func postInstallerTelemetry(ctx context.Context, endpoint string, payload installerTelemetryPayload, client *http.Client) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZHC-Installer/"+installerVersion)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("installer telemetry returned HTTP %d", response.StatusCode)
	}
	return nil
}

func installerDiagnosticDirectory() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ZHCASH-Installer", "diagnostics"), nil
}

func writeInstallerDiagnosticReport(diagnosticDir string, payload installerTelemetryPayload) (string, error) {
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(diagnosticDir, 0o700); err != nil {
		return "", err
	}
	name := "zhc-installer-diagnostics-" + time.Now().UTC().Format("20060102-150405") + ".json"
	path := filepath.Join(diagnosticDir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
