package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMacOSRealSignedPayload(t *testing.T) {
	archive := os.Getenv("ZHC_TEST_NODE_ARCHIVE")
	if archive == "" || runtime.GOOS != "darwin" {
		t.Skip("requires locally built signed macOS payload")
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	dest := t.TempDir()
	if err := installMacOSArchive(archive, hex.EncodeToString(digest[:]), dest); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dest, macOSAppName)
	if out, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
		t.Fatalf("signature after Go extraction: %v %s", err, out)
	}
	if out, err := exec.Command(filepath.Join(app, "Contents", "MacOS", "zerohour-qt"), "-version").CombinedOutput(); err != nil || !bytes.Contains(out, []byte("1.0.0")) {
		t.Fatalf("node version: %v %s", err, out)
	}
}

func testMacOSArchive(t *testing.T) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evolution-macos.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, body := range map[string]string{"Contents/MacOS/zerohour-qt": "fixture executable", "Contents/Info.plist": "fixture plist"} {
		h := &zip.FileHeader{Name: macOSAppName + "/" + name, Method: zip.Deflate}
		h.SetMode(0755)
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(b)
	return p, hex.EncodeToString(digest[:])
}

func TestMacOSPackageInstallAndReplace(t *testing.T) {
	archive, digest := testMacOSArchive(t)
	dest := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := installMacOSArchive(archive, digest, dest); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dest, macOSAppName, "Contents/MacOS/zerohour-qt"))
		if err != nil || string(b) != "fixture executable" {
			t.Fatalf("bad installed payload: %q %v", b, err)
		}
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatal("staging entries leaked")
	}
}

func TestMacOSTamperedPackagePreservesExistingApp(t *testing.T) {
	archive, digest := testMacOSArchive(t)
	dest := t.TempDir()
	marker := filepath.Join(dest, macOSAppName, "old-wallet-independent-marker")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installMacOSArchive(archive, digest, dest); err == nil {
		t.Fatal("accepted wrong hash")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatal("changed old app")
	}
}

func TestJSONByteProgress(t *testing.T) {
	var output bytes.Buffer
	eventOutput = &output
	defer func() { eventOutput = nil }()
	p := byteProgress{phase: "snapshot_extract", total: 7}
	_, _ = p.Write([]byte("abc"))
	_, _ = p.Write([]byte("defg"))
	decoder := json.NewDecoder(&output)
	var last progressEvent
	for decoder.More() {
		if err := decoder.Decode(&last); err != nil {
			t.Fatal(err)
		}
	}
	if last.Done != 7 || last.Total != 7 || last.Phase != "snapshot_extract" {
		t.Fatalf("wrong counters: %+v", last)
	}
}

func TestSnapshotHashEvents(t *testing.T) {
	p := filepath.Join(t.TempDir(), defaultOutputName)
	_ = os.WriteFile(p, []byte("test"), 0600)
	digest := sha256.Sum256([]byte("test"))
	var output bytes.Buffer
	eventOutput = &output
	defer func() { eventOutput = nil }()
	if err := verifySHA256File(p, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"phase":"snapshot_verify"`)) || !bytes.Contains(output.Bytes(), []byte(`"done":4`)) {
		t.Fatal(output.String())
	}
}
