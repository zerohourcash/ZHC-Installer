//go:build darwin

package main

import (
	"archive/zip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func stopMacOSNodeService() error {
	target := fmt.Sprintf("gui/%d/st.zeroscash.zerohourd", os.Getuid())
	probe := exec.Command("/bin/launchctl", "print", target)
	if err := probe.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 113 {
			return nil
		}
		return fmt.Errorf("cannot inspect managed node service %s: %w", target, err)
	}
	fmt.Println("Stopping managed macOS node service (keeping its plist):", target)
	if out, err := exec.Command("/bin/launchctl", "bootout", target).CombinedOutput(); err != nil {
		return fmt.Errorf("stop managed macOS node: %w: %s", err, out)
	}
	return nil
}

func lockMacOSInstaller() (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "Library", "Application Support", "ZHC Installer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "install.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another macOS installer is already running: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func checkMacOSExtractionSpace(archive, dataDir string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	needed := uint64(2 * 1024 * 1024 * 1024) // Additional room for node, indexes and filesystem overhead.
	for _, f := range z.File {
		if preserveSnapshotTarget(f.Name) {
			continue
		}
		if f.UncompressedSize64 > (1<<63)-needed {
			return fmt.Errorf("snapshot size overflow")
		}
		needed += f.UncompressedSize64
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &stat); err != nil {
		return err
	}
	free := stat.Bavail * uint64(stat.Bsize)
	if free < needed {
		return fmt.Errorf("snapshot extraction needs %s free; available %s; old blockchain has not been cleaned", humanBytes(int64(needed)), humanBytes(int64(free)))
	}
	return nil
}
