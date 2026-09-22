package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Only a unique installer-owned directory is removed; the Desktop is never cleaned.
func nodeReleaseStagingDir(tempRoot string, desktop func() (string, error)) (string, error) {
	dir, tempErr := os.MkdirTemp(tempRoot, "zhcash-node-release-*")
	if tempErr == nil {
		return dir, nil
	}
	root, err := desktop()
	if err != nil {
		return "", fmt.Errorf("temporary folder unavailable (%v); cannot locate Desktop: %w", tempErr, err)
	}
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("temporary folder unavailable (%v); invalid Desktop path %q", tempErr, root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("temporary folder unavailable (%v); cannot create Desktop %q: %w", tempErr, root, err)
	}
	dir, err = os.MkdirTemp(root, "ZHC-Installer-*")
	if err != nil {
		return "", fmt.Errorf("temporary folder unavailable (%v); cannot create installation folder on Desktop %q: %w", tempErr, root, err)
	}
	fmt.Println("Windows temporary folder is unavailable. Using Desktop installation folder:", dir)
	return dir, nil
}

func installWindowsNodeArchive(archive, stage, nodeDir string) (string, error) {
	extracted, err := extractSingleFileFromZip(archive, "zerohour-qt.exe", stage)
	if err != nil {
		return "", err
	}
	source, err := os.Open(extracted)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target := filepath.Join(nodeDir, "zerohour-qt.exe")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		return "", err
	}
	if err := writeFileFromReader(target, source, 0o755); err != nil {
		return "", err
	}
	return target, nil
}
