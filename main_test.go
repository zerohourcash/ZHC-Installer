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
	mustWrite(t, filepath.Join(dir, "blocks", "blk00000.dat"), "block")
	mustWrite(t, filepath.Join(dir, "chainstate", "000001.ldb"), "state")
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
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("expected wallet path to be preserved: %s: %v", kept, err)
		}
	}
	for _, removedPath := range []string{
		filepath.Join(dir, "blocks"),
		filepath.Join(dir, "chainstate"),
		filepath.Join(dir, "debug.log"),
	} {
		if _, err := os.Stat(removedPath); !os.IsNotExist(err) {
			t.Fatalf("expected path to be removed: %s", removedPath)
		}
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

func TestNodeProcessNamesByOS(t *testing.T) {
	if got := strings.Join(nodeProcessNames("windows"), ","); got != "zerohour-qt.exe,zerohourd.exe,zerohour-cli.exe" {
		t.Fatalf("unexpected windows process names: %s", got)
	}
	if got := strings.Join(nodeProcessNames("linux"), ","); got != "zerohour-qt,zerohourd,zerohour-cli" {
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
