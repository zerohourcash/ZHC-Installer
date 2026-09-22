package main

import (
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
