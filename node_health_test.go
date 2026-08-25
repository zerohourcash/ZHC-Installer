package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadLocalNodeRPCSettingsUsesLoopbackAndCredentials(t *testing.T) {
	dir := t.TempDir()
	config := "rpcport=43123\nrpcuser=installer-user\nrpcpassword=installer-secret\n"
	if err := os.WriteFile(filepath.Join(dir, "zerohour.conf"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := readLocalNodeRPCSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if settings.URL != "http://127.0.0.1:43123/" || settings.Username != "installer-user" || settings.Password != "installer-secret" {
		t.Fatalf("unexpected RPC settings: %#v", settings)
	}
}

func TestWaitForLocalNodeReadyRetriesWarmupAndChecksBestBlock(t *testing.T) {
	var blockCountCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "operator" || password != "secret" {
			t.Errorf("unexpected basic auth: %q %q %v", username, password, ok)
		}
		var rpcRequest struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Fatal(err)
		}
		response.Header().Set("Content-Type", "application/json")
		switch rpcRequest.Method {
		case "getblockcount":
			if blockCountCalls.Add(1) < 3 {
				_ = json.NewEncoder(response).Encode(map[string]any{"result": nil, "error": map[string]any{"code": -28, "message": "Loading block index"}})
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"result": 1682017, "error": nil})
		case "getbestblockhash":
			_ = json.NewEncoder(response).Encode(map[string]any{"result": strings.Repeat("a", 64), "error": nil})
		case "getconnectioncount":
			_ = json.NewEncoder(response).Encode(map[string]any{"result": 8, "error": nil})
		default:
			t.Errorf("unexpected RPC method: %s", rpcRequest.Method)
		}
	}))
	defer server.Close()

	listenerAddress := server.Listener.Addr().(*net.TCPAddr)
	dir := t.TempDir()
	config := "rpcport=" + strconv.Itoa(listenerAddress.Port) + "\nrpcuser=operator\nrpcpassword=secret\n"
	if err := os.WriteFile(filepath.Join(dir, "zerohour.conf"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	ready, err := waitForLocalNodeReady(context.Background(), dir, time.Second, 5*time.Millisecond, &output)
	if err != nil {
		t.Fatal(err)
	}
	if ready.BlockHeight != 1682017 || ready.BestBlockHash != strings.Repeat("a", 64) || ready.Connections != 8 {
		t.Fatalf("unexpected readiness: %#v", ready)
	}
	if blockCountCalls.Load() != 3 || !strings.Contains(output.String(), "Waiting for local ZHC RPC") {
		t.Fatalf("warmup was not retried visibly: calls=%d output=%q", blockCountCalls.Load(), output.String())
	}
}

func TestCollectNodeDiagnosticsTailsAndRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	debug := strings.Repeat("old line\n", 10000) + "rpcuser=alice rpcpassword=top-secret token=abc123\nC:\\Users\\Yoga\\ZHCASH\nstartup failed\n"
	if err := os.WriteFile(filepath.Join(dir, "debug.log"), []byte(debug), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := collectNodeDiagnostics(dir)
	if len(logs) != 1 || logs[0].Name != "debug.log" {
		t.Fatalf("unexpected diagnostic files: %#v", logs)
	}
	if strings.Contains(logs[0].Tail, "top-secret") || strings.Contains(logs[0].Tail, "abc123") || strings.Contains(logs[0].Tail, "rpcuser=alice") {
		t.Fatalf("diagnostics leaked a secret: %q", logs[0].Tail)
	}
	if !strings.Contains(logs[0].Tail, "startup failed") || len(logs[0].Tail) > int(maxNodeDiagnosticTailBytes) {
		t.Fatalf("diagnostic tail is invalid: size=%d", len(logs[0].Tail))
	}
}

func TestWaitForLocalNodeReadyReturnsTypedTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"result": nil,
			"error":  map[string]any{"code": -28, "message": "Loading block index"},
		})
	}))
	defer server.Close()
	listenerAddress := server.Listener.Addr().(*net.TCPAddr)
	dir := t.TempDir()
	config := "rpcport=" + strconv.Itoa(listenerAddress.Port) + "\nrpcuser=operator\nrpcpassword=secret\n"
	if err := os.WriteFile(filepath.Join(dir, "zerohour.conf"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := waitForLocalNodeReady(context.Background(), dir, 100*time.Millisecond, 5*time.Millisecond, io.Discard)
	if !errors.Is(err, errNodeStartupTimeout) || !strings.Contains(err.Error(), "Loading block index") {
		t.Fatalf("unexpected startup timeout: %v", err)
	}
}

func TestWriteInstallerDiagnosticReportUsesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diagnostics")
	payload := newInstallerTelemetryPayload("failure", time.Second, nodeReadiness{}, errors.New("start failed"), nil)
	reportPath, err := writeInstallerDiagnosticReport(dir, payload)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("diagnostic report is not private: %o", info.Mode().Perm())
	}
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "zhc_installer_failure") || !strings.Contains(string(content), "start failed") {
		t.Fatalf("unexpected diagnostic report: %s", content)
	}
}

func TestPostInstallerTelemetryUsesPWAFeedbackShape(t *testing.T) {
	var received installerTelemetryPayload
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/feedback" || request.Method != http.MethodPost {
			t.Fatalf("unexpected telemetry request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("User-Agent") != "ZHC-Installer/"+installerVersion {
			t.Fatalf("unexpected user agent: %q", request.Header.Get("User-Agent"))
		}
		if err := json.NewDecoder(io.LimitReader(request.Body, 96*1024)).Decode(&received); err != nil {
			t.Fatal(err)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	payload := newInstallerTelemetryPayload("success", 12*time.Second, nodeReadiness{
		BlockHeight: 1682017, BestBlockHash: strings.Repeat("b", 64), Connections: 4, RPCMethod: "getblockcount+getbestblockhash",
	}, nil, nil)
	if err := postInstallerTelemetry(context.Background(), server.URL+"/feedback", payload, server.Client()); err != nil {
		t.Fatal(err)
	}
	if received.Message != "zhc_installer: node installation completed and local RPC is ready" || received.Diagnostics.Reason != "zhc_installer_success" || received.Client.Kind != "zhc-installer" {
		t.Fatalf("unexpected feedback payload: %#v", received)
	}
}

func TestOfficialInstallerTelemetryRejectsNonOfficialEndpoint(t *testing.T) {
	payload := newInstallerTelemetryPayload("success", time.Second, nodeReadiness{}, nil, nil)
	if err := sendInstallerTelemetry(context.Background(), "http://wallet.zeroscan.st/feedback", payload); err == nil {
		t.Fatal("plain HTTP telemetry must be rejected")
	}
	if err := sendInstallerTelemetry(context.Background(), "https://example.com/feedback", payload); err == nil {
		t.Fatal("non-official telemetry endpoint must be rejected")
	}
}
