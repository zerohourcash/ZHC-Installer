package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsDesktopKnownFolder(t *testing.T) {
	path, err := windowsDesktopDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("Desktop must be absolute: %q", path)
	}
}

func TestWindowsStagingWithExpiredSessionTemp(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "Temp", "2")
	t.Setenv("TEMP", missing)
	t.Setenv("TMP", missing)
	desktop, err := windowsDesktopDirectory()
	if err != nil {
		t.Fatal(err)
	}
	stage, err := nodeReleaseStagingDir(os.TempDir(), windowsDesktopDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if filepath.Dir(stage) != desktop {
		t.Fatalf("expected real Desktop, got %q", stage)
	}
	if err := os.WriteFile(filepath.Join(stage, "probe"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
}
