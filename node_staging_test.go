package main

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeStagingUsesWorkingTemp(t *testing.T) {
	root := t.TempDir()
	stage, err := nodeReleaseStagingDir(root, func() (string, error) { t.Fatal("Desktop must not be queried when TEMP works"); return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(stage) != root {
		t.Fatalf("unexpected stage: %s", stage)
	}
}

func TestNodeStagingDesktopFallbackAndExtraction(t *testing.T) {
	for _, failure := range []string{"missing", "not-directory"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			temp := filepath.Join(root, "Temp", "2")
			if failure == "not-directory" {
				temp = filepath.Join(root, "blocked")
				if err := os.WriteFile(temp, []byte("file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			desktop := filepath.Join(root, "OneDrive", "Рабочий стол")
			stage, err := nodeReleaseStagingDir(temp, func() (string, error) { return desktop, nil })
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(stage) != desktop {
				t.Fatal(stage)
			}
			marker := filepath.Join(desktop, "user-document.txt")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(stage, "node.zip")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			w := zip.NewWriter(f)
			entry, err := w.Create("release/zerohour-qt.exe")
			if err != nil {
				t.Fatal(err)
			}
			entry.Write([]byte("test-node"))
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			dest := filepath.Join(root, "node")
			installed, err := installWindowsNodeArchive(archive, stage, dest)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(installed)
			if err != nil || string(data) != "test-node" {
				t.Fatalf("installed node: %q %v", data, err)
			}
			if err := os.RemoveAll(stage); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve" {
				t.Fatalf("Desktop document changed: %v", err)
			}
			if _, err := os.Stat(installed); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNodeStagingBothLocationsFail(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	os.WriteFile(blocked, []byte("file"), 0600)
	_, err := nodeReleaseStagingDir(filepath.Join(root, "missing"), func() (string, error) { return blocked, nil })
	if err == nil || !strings.Contains(err.Error(), "Desktop") {
		t.Fatalf("expected actionable error: %v", err)
	}
	_, err = nodeReleaseStagingDir(filepath.Join(root, "missing"), func() (string, error) { return "", errors.New("resolver failure") })
	if err == nil || !strings.Contains(err.Error(), "resolver failure") {
		t.Fatal(err)
	}
}
