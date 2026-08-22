package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeriveMegaCryptoParams(t *testing.T) {
	params, err := deriveMegaCryptoParams("8avoKJxzjLjfgj2SbhBrqMo-FCqt-i2myM1XQZy49Gg")
	if err != nil {
		t.Fatal(err)
	}
	if got := bytesToHex(params.Key); got != "3b95fc023189a11e174f6ad3f2a89fc0" {
		t.Fatalf("unexpected AES key: %s", got)
	}
	if got := bytesToHex(params.IV); got != "ca3e142aadfa2da60000000000000000" {
		t.Fatalf("unexpected AES IV: %s", got)
	}
}

func TestNewCTRAtOffsetMatchesFullStreamSlice(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("abcdef9876543210")
	plain := bytes.Repeat([]byte("ZHCASH"), 100)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := append([]byte(nil), plain...)
	cipher.NewCTR(block, iv).XORKeyStream(encrypted, encrypted)

	const offset = 37
	want := plain[offset:]
	got := append([]byte(nil), encrypted[offset:]...)
	stream := newCTRAtOffset(block, iv, offset)
	stream.XORKeyStream(got, got)

	if !bytes.Equal(got, want) {
		t.Fatal("offset CTR decryption did not match full stream slice")
	}
}

func TestResolveSourcesAutoOrder(t *testing.T) {
	sources, err := resolveSources("auto")
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceNames(sources); !bytes.Equal([]byte(got), []byte("yandex,mega,github,zeroscan")) {
		t.Fatalf("unexpected auto source order: %s", got)
	}
}

func TestResolveSourcesSingleSource(t *testing.T) {
	t.Setenv(yandexURLVariable, "https://example.invalid/secret-yandex-link")

	sources, err := resolveSources("yandex")
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceNames(sources); got != "yandex" {
		t.Fatalf("unexpected single source: %s", got)
	}
	if sources[0].URL != "https://example.invalid/secret-yandex-link" {
		t.Fatal("expected yandex URL to be read from environment")
	}
}

func TestConfiguredYandexURLUsesObfuscatedPayloadBeforeEnvironment(t *testing.T) {
	oldPayload, oldKey := yandexURLPayload, yandexURLKey
	defer func() {
		yandexURLPayload, yandexURLKey = oldPayload, oldKey
	}()

	t.Setenv(yandexURLVariable, "https://example.invalid/env-link")
	payload, key := obfuscateForTest("https://example.invalid/embedded-link", "test-key")
	yandexURLPayload = payload
	yandexURLKey = key

	if got := configuredYandexURL(); got != "https://example.invalid/embedded-link" {
		t.Fatalf("unexpected configured yandex URL: %s", got)
	}
}

func TestConfiguredYandexURLFallsBackToEnvironment(t *testing.T) {
	oldPayload, oldKey := yandexURLPayload, yandexURLKey
	defer func() {
		yandexURLPayload, yandexURLKey = oldPayload, oldKey
	}()

	yandexURLPayload = ""
	yandexURLKey = ""
	t.Setenv(yandexURLVariable, "https://example.invalid/env-link")

	if got := configuredYandexURL(); got != "https://example.invalid/env-link" {
		t.Fatalf("unexpected configured yandex URL: %s", got)
	}
}

func TestResolveSourcesRejectsUnknownSource(t *testing.T) {
	if _, err := resolveSources("unknown"); err == nil {
		t.Fatal("expected unknown source to be rejected")
	}
}

func TestGithubSnapshotPartURLs(t *testing.T) {
	urls := githubSnapshotPartURLs()
	if len(urls) != 10 {
		t.Fatalf("unexpected github part count: %d", len(urls))
	}
	if !strings.HasSuffix(urls[0], "/zhcash-node-seed.zip.part01") {
		t.Fatalf("unexpected first github part URL: %s", urls[0])
	}
	if !strings.HasSuffix(urls[9], "/zhcash-node-seed.zip.part10") {
		t.Fatalf("unexpected last github part URL: %s", urls[9])
	}
}

func TestCleanBlockchainDataPreservesWalletFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "wallet.dat"), "wallet")
	mustWrite(t, filepath.Join(dir, "wallet", "nested.dat"), "wallet-dir")
	mustWrite(t, filepath.Join(dir, "wallets", "nested.dat"), "wallets-dir")
	mustWrite(t, filepath.Join(dir, "zerohour.conf"), "rpcuser=local")
	mustWrite(t, filepath.Join(dir, "custom.CONF"), "custom=local")
	mustWrite(t, filepath.Join(dir, "wallet-copy.bak"), "backup")
	mustWrite(t, filepath.Join(dir, defaultOutputName), "snapshot")
	mustWrite(t, filepath.Join(dir, defaultOutputName+".part"), "partial")
	mustWrite(t, filepath.Join(dir, "blocks", "blk00000.dat"), "block")
	mustWrite(t, filepath.Join(dir, "chainstate", "000001.ldb"), "state")
	mustWrite(t, filepath.Join(dir, "database", "log.0000000001"), "database")
	mustWrite(t, filepath.Join(dir, "stateZHCASH", "state.dat"), "contract-state")
	mustWrite(t, filepath.Join(dir, "indexes", "txindex", "000001.ldb"), "index")
	mustWrite(t, filepath.Join(dir, "peers.dat"), "peers")
	mustWrite(t, filepath.Join(dir, "mempool.dat"), "mempool")
	mustWrite(t, filepath.Join(dir, "debug.log"), "log")

	removed, err := cleanBlockchainData(dir)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("expected blockchain data to be removed")
	}
	for _, kept := range []string{
		filepath.Join(dir, "wallet.dat"),
		filepath.Join(dir, "wallet", "nested.dat"),
		filepath.Join(dir, "wallets", "nested.dat"),
		filepath.Join(dir, "zerohour.conf"),
		filepath.Join(dir, "custom.CONF"),
		filepath.Join(dir, "wallet-copy.bak"),
		filepath.Join(dir, defaultOutputName),
		filepath.Join(dir, defaultOutputName+".part"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("expected wallet path to be preserved: %s: %v", kept, err)
		}
	}
	for _, removedPath := range []string{
		filepath.Join(dir, "blocks"),
		filepath.Join(dir, "chainstate"),
		filepath.Join(dir, "database"),
		filepath.Join(dir, "stateZHCASH"),
		filepath.Join(dir, "indexes"),
		filepath.Join(dir, "peers.dat"),
		filepath.Join(dir, "mempool.dat"),
		filepath.Join(dir, "debug.log"),
	} {
		if _, err := os.Stat(removedPath); !os.IsNotExist(err) {
			t.Fatalf("expected path to be removed: %s", removedPath)
		}
	}
}

func TestExtractSnapshotDoesNotOverwriteWalletOrConfiguration(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "snapshot.zip")
	destination := filepath.Join(dir, "data")
	mustWrite(t, filepath.Join(destination, "wallet.dat"), "local-wallet")
	mustWrite(t, filepath.Join(destination, "wallet", "nested.dat"), "local-wallet-dir")
	mustWrite(t, filepath.Join(destination, "zerohour.conf"), "rpcuser=local")
	mustWrite(t, filepath.Join(destination, "wallet-copy.bak"), "local-backup")
	createZip(t, archive, map[string]string{
		"blocks/blk00000.dat":   "snapshot-block",
		"chainstate/000001.ldb": "snapshot-state",
		"wallet.dat":            "snapshot-wallet",
		"wallet/nested.dat":     "snapshot-wallet-dir",
		"./zerohour.conf":       "rpcuser=snapshot",
		"wallet-copy.bak":       "snapshot-backup",
	})

	if err := extractZipArchive(archive, destination); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		"wallet.dat":          "local-wallet",
		"wallet/nested.dat":   "local-wallet-dir",
		"zerohour.conf":       "rpcuser=local",
		"wallet-copy.bak":     "local-backup",
		"blocks/blk00000.dat": "snapshot-block",
	} {
		content, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(content) != expected {
			t.Fatalf("unexpected content for %s: got %q, want %q", path, content, expected)
		}
	}
}

func TestStopManagedNodeServiceIgnoresMissingSystemctl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := stopManagedNodeService("linux"); err != nil {
		t.Fatalf("missing systemctl should be ignored: %v", err)
	}
}

func TestStopManagedNodeServiceStopsActiveService(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	systemctlPath := filepath.Join(dir, "systemctl")
	mustWrite(t, systemctlPath, `#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMCTL_TEST_LOG"
case "$1" in
  is-active) exit 0 ;;
  stop) exit 0 ;;
esac
exit 1
`)
	if err := os.Chmod(systemctlPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("SYSTEMCTL_TEST_LOG", logPath)

	if err := stopManagedNodeService("linux"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "is-active --quiet zerohourd.service\nstop zerohourd.service\n" {
		t.Fatalf("unexpected systemctl calls: %q", got)
	}
}

func TestRemoveSnapshotArchiveDeletesZipOnly(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, defaultOutputName)
	partial := archive + ".part"
	mustWrite(t, archive, "snapshot")
	mustWrite(t, partial, "partial")

	if err := removeSnapshotArchive(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("expected snapshot archive to be deleted")
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("partial file should not be removed by archive cleanup: %v", err)
	}
}

func TestShouldRemoveSnapshotArchive(t *testing.T) {
	if !shouldRemoveSnapshotArchive(false) {
		t.Fatal("expected archive removal by default")
	}
	if shouldRemoveSnapshotArchive(true) {
		t.Fatal("expected archive to be kept when requested")
	}
}

func TestPrepareExistingSnapshotArchiveKeepsValidZipAndDeletesPartial(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, defaultOutputName)
	partial := archive + ".part"
	content := []byte("valid snapshot")
	mustWrite(t, archive, string(content))
	mustWrite(t, partial, "old partial")
	sum := sha256.Sum256(content)

	valid, err := prepareExistingSnapshotArchive(archive, int64(len(content)), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("expected existing archive to be accepted")
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("expected valid archive to remain: %v", err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("expected old partial to be deleted")
	}
}

func TestPrepareExistingSnapshotArchiveDeletesInvalidZipAndPartial(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, defaultOutputName)
	partial := archive + ".part"
	mustWrite(t, archive, "short")
	mustWrite(t, partial, "old partial")

	valid, err := prepareExistingSnapshotArchive(archive, 100, strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("expected invalid archive to be rejected")
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("expected invalid archive to be deleted")
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("expected partial to be deleted")
	}
}

func TestFindNodeExecutableFindsNestedQt(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "zhcash-evolution-1.0.0-win64", "bin", "zerohour-qt.exe")
	mustWrite(t, exe, "exe")

	got, err := findNodeExecutable("windows", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != exe {
		t.Fatalf("unexpected executable path: got %s want %s", got, exe)
	}
}

func TestDefaultDataDirByOS(t *testing.T) {
	env := map[string]string{
		"APPDATA": "/Users/Alice/AppData/Roaming",
		"HOME":    "/home/alice",
	}
	cases := []struct {
		goos string
		want string
	}{
		{"windows", filepath.Join("/Users/Alice/AppData/Roaming", "ZHCASH")},
		{"linux", filepath.Join("/home/alice", ".zerohour")},
		{"darwin", filepath.Join("/home/alice", "Library", "Application Support", "ZHCASH")},
	}
	for _, tc := range cases {
		got, err := defaultDataDir(tc.goos, env)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("%s default datadir: got %s want %s", tc.goos, got, tc.want)
		}
	}
}

func TestDefaultDataDirPrefersZHCASHEnv(t *testing.T) {
	env := map[string]string{
		dataDirVariable: "/custom/zhcash-data",
		"APPDATA":       "/Users/Alice/AppData/Roaming",
		"HOME":          "/home/alice",
	}

	got, err := defaultDataDir("windows", env)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/zhcash-data" {
		t.Fatalf("expected %s to win, got %s", dataDirVariable, got)
	}
}

func TestDefaultNodeDirPrefersZHCASHEnv(t *testing.T) {
	env := map[string]string{
		nodeDirVariable: "/custom/zhcash-node",
		"USERPROFILE":   "/Users/Alice",
	}

	got, err := defaultNodeDir("windows", env, "/run")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/zhcash-node" {
		t.Fatalf("expected %s to win, got %s", nodeDirVariable, got)
	}
}

func TestDefaultNodeDirLinuxServerUsesHomeZHCASH(t *testing.T) {
	env := map[string]string{
		"HOME": "/root",
	}

	got, err := defaultNodeDir("linux", env, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/root", "ZHCASH") {
		t.Fatalf("unexpected linux server node dir: %s", got)
	}
}

func TestDefaultNodeDirLinuxGUIUsesRunDir(t *testing.T) {
	env := map[string]string{
		"HOME":                "/home/alice",
		"XDG_CURRENT_DESKTOP": "GNOME",
	}

	got, err := defaultNodeDir("linux", env, "/opt/installer")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/installer" {
		t.Fatalf("unexpected linux GUI node dir: %s", got)
	}
}

func TestIsLinuxServer(t *testing.T) {
	if !isLinuxServer("linux", map[string]string{"HOME": "/root"}) {
		t.Fatal("linux without display should be server mode")
	}
	if !isLinuxServer("linux", map[string]string{"DISPLAY": "localhost:10.0"}) {
		t.Fatal("ssh X11 forwarding DISPLAY alone should stay server mode")
	}
	if isLinuxServer("linux", map[string]string{"XDG_CURRENT_DESKTOP": "GNOME"}) {
		t.Fatal("linux with XDG_CURRENT_DESKTOP should be GUI mode")
	}
	if isLinuxServer("linux", map[string]string{"DESKTOP_SESSION": "ubuntu"}) {
		t.Fatal("linux with DESKTOP_SESSION should be GUI mode")
	}
	if isLinuxServer("linux", map[string]string{"GDMSESSION": "gnome"}) {
		t.Fatal("linux with GDMSESSION should be GUI mode")
	}
	if isLinuxServer("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}) {
		t.Fatal("linux with WAYLAND_DISPLAY should be GUI mode")
	}
	if isLinuxServer("windows", map[string]string{}) {
		t.Fatal("windows must not be linux server mode")
	}
}

func TestZerohourdServiceUnit(t *testing.T) {
	unit := zerohourdServiceUnit("/root/ZHCASH/bin/zerohourd", "/root/.zerohour")

	for _, want := range []string{
		"ExecStart=/root/ZHCASH/bin/zerohourd -datadir=/root/.zerohour",
		"Restart=always",
		"RestartSec=10",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("service unit missing %q:\n%s", want, unit)
		}
	}
}

func TestFindNodeExecutableFindsNestedDaemon(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "zhcash-evolution-1.0.0-linux-x86_64", "bin", "zerohourd")
	mustWrite(t, exe, "daemon")

	got, err := findNamedExecutable("linux", dir, "zerohourd")
	if err != nil {
		t.Fatal(err)
	}
	if got != exe {
		t.Fatalf("unexpected daemon path: got %s want %s", got, exe)
	}
}

func TestMissingEnvironmentUpdates(t *testing.T) {
	updates := missingEnvironmentUpdates(map[string]string{}, "/data", "/node")

	if len(updates) != 2 {
		t.Fatalf("unexpected update count: %d", len(updates))
	}
	if updates[0].Name != dataDirVariable || updates[0].Value != "/data" {
		t.Fatalf("unexpected data dir update: %#v", updates[0])
	}
	if updates[1].Name != nodeDirVariable || updates[1].Value != "/node" {
		t.Fatalf("unexpected node dir update: %#v", updates[1])
	}
}

func TestMissingEnvironmentUpdatesSkipsExisting(t *testing.T) {
	updates := missingEnvironmentUpdates(map[string]string{
		dataDirVariable: "/existing-data",
	}, "/data", "/node")

	if len(updates) != 1 {
		t.Fatalf("unexpected update count: %d", len(updates))
	}
	if updates[0].Name != nodeDirVariable {
		t.Fatalf("unexpected update: %#v", updates[0])
	}
}

func TestNodeProcessNamesByOS(t *testing.T) {
	if got := strings.Join(nodeProcessNames("windows"), ","); got != "zerohour-qt.exe,zerohourd.exe" {
		t.Fatalf("unexpected windows process names: %s", got)
	}
	if got := strings.Join(nodeProcessNames("linux"), ","); got != "zerohour-qt,zerohourd" {
		t.Fatalf("unexpected linux process names: %s", got)
	}
}

func TestExtractSnapshotRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "snapshot.zip")
	createZip(t, archive, map[string]string{
		"../evil.txt": "evil",
	})

	err := extractZipArchive(archive, filepath.Join(dir, "data"))
	if err == nil {
		t.Fatal("expected path traversal zip entry to be rejected")
	}
}

func TestExtractWindowsQtFromNestedReleaseZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "node.zip")
	createZip(t, archive, map[string]string{
		"zhcash-evolution-1.0.0-win64/README.txt":       "readme",
		"zhcash-evolution-1.0.0-win64/zerohour-cli.exe": "cli",
		"zhcash-evolution-1.0.0-win64/zerohour-qt.exe":  "qt",
	})

	target, err := extractSingleFileFromZip(archive, "zerohour-qt.exe", dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(target) != "zerohour-qt.exe" {
		t.Fatalf("unexpected target filename: %s", target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "qt" {
		t.Fatalf("unexpected extracted content: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "zerohour-cli.exe")); !os.IsNotExist(err) {
		t.Fatal("should not extract unrelated exe")
	}
}

func TestSnapshotPartPathIsSharedAcrossMirrors(t *testing.T) {
	output := filepath.Join(t.TempDir(), "zhcash-node-seed.zip")

	if got := snapshotPartPath(output); got != output+".part" {
		t.Fatalf("unexpected snapshot partial path: %s", got)
	}
}

func TestVerifySHA256File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.zip")
	content := []byte("snapshot")
	mustWrite(t, path, string(content))
	sum := sha256.Sum256(content)

	if err := verifySHA256File(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySHA256FileRejectsMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.zip")
	mustWrite(t, path, "snapshot")

	if err := verifySHA256File(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestFinalizeCompletePartRenamesFullPart(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "snapshot.zip")
	part := output + ".part"
	createZip(t, part, map[string]string{"blocks/blk00000.dat": "block"})
	stat, err := os.Stat(part)
	if err != nil {
		t.Fatal(err)
	}

	done, err := finalizeCompletePart(output, part, stat.Size(), verifyZipHeader)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("expected full partial file to be finalized")
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("expected output file after finalize: %v", err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("expected partial file to be renamed away")
	}
}

func TestFinalizeCompletePartIgnoresIncompletePart(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "snapshot.zip")
	part := output + ".part"
	mustWrite(t, part, "partial")

	done, err := finalizeCompletePart(output, part, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("incomplete partial file must not be finalized")
	}
}

func TestResumeOffsetDeletesOversizedPart(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, "snapshot.zip.part")
	mustWrite(t, part, "oversized partial")

	offset, err := resumeOffset(part, 5)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatalf("expected resume offset 0 after deleting oversized partial, got %d", offset)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("expected oversized partial file to be removed")
	}
}

func TestPlainHTTPDownloadFinalizesFullPartWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "snapshot.zip")
	part := output + ".part"
	createZip(t, part, map[string]string{"blocks/blk00000.dat": "block"})
	stat, err := os.Stat(part)
	if err != nil {
		t.Fatal(err)
	}

	err = downloadPlainHTTPFileWithSize(context.Background(), "http://127.0.0.1:1/unreachable.zip", output, part, stat.Size(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("expected output file after finalize: %v", err)
	}
}

func TestIdleWatchdogCancelsAfterNoProgress(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	watchdog := newIdleWatchdog(10*time.Millisecond, cancel)
	defer watchdog.stop()

	time.Sleep(50 * time.Millisecond)

	if !errors.Is(context.Cause(ctx), errIdleTimeout) {
		t.Fatalf("expected idle timeout cause, got %v", context.Cause(ctx))
	}
}

func sourceNames(sources []sourceConfig) string {
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.Name
	}
	return strings.Join(names, ",")
}

func obfuscateForTest(value string, key string) (string, string) {
	data := []byte(value)
	keyBytes := []byte(key)
	for i := range data {
		data[i] ^= keyBytes[i%len(keyBytes)]
	}
	return base64.StdEncoding.EncodeToString(data), base64.StdEncoding.EncodeToString(keyBytes)
}

func createZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bytesToHex(data []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(data)*2)
	for i, b := range data {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}
