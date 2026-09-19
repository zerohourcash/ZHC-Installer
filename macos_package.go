package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Set at build time from the signed, bundled node archive, never from its contents.
var packagedNodeSHA256 string
var macOSDesktopMode bool

const macOSAppName = "ZHCASH Evolution.app"

func packagedNodePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "..", "Resources", "evolution-macos.zip"), nil
}

func verifyPackagedNode() (string, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return "", errors.New("this desktop installer requires macOS on Apple Silicon")
	}
	if len(packagedNodeSHA256) != 64 {
		return "", errors.New("macOS node package was not embedded by the build script")
	}
	archive, err := packagedNodePath()
	if err != nil {
		return "", err
	}
	if err := verifySHA256File(archive, packagedNodeSHA256); err != nil {
		return "", fmt.Errorf("bundled Evolution validation: %w", err)
	}
	return archive, nil
}

func installMacOSArchive(archive, digest, nodeDir string) error {
	if err := verifySHA256File(archive, digest); err != nil {
		return err
	}
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(nodeDir, ".zhcash-install-")
	if err != nil {
		return err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := extractZipArchive(archive, stage); err != nil {
		return err
	}
	app := filepath.Join(stage, macOSAppName)
	executable := filepath.Join(app, "Contents", "MacOS", "zerohour-qt")
	info, err := os.Lstat(executable)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("bundled node executable is not a regular executable file")
	}
	if _, err := os.Stat(filepath.Join(app, "Contents", "Info.plist")); err != nil {
		return err
	}
	target := filepath.Join(nodeDir, macOSAppName)
	backup := filepath.Join(stage, "previous.app")
	hadPrevious := false
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(app, target); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				// Retain the backup outside the temporary directory on rollback failure.
				recovery := filepath.Join(nodeDir, "ZHCASH Evolution.recovery.app")
				if moveErr := os.Rename(backup, recovery); moveErr != nil {
					keepStage = true
					return fmt.Errorf("install: %v; rollback: %v; recovery move: %v", err, restoreErr, moveErr)
				}
				return fmt.Errorf("install failed: %v; previous application retained at %s", err, recovery)
			}
		}
		return err
	}
	return nil
}

func installBundledMacOSNode() error {
	archive, err := verifyPackagedNode()
	if err != nil {
		return err
	}
	return installMacOSArchive(archive, packagedNodeSHA256, desktopNodeDir)
}

var desktopNodeDir string

func startMacOSApp(ctx context.Context, nodeDir, dataDir string) error {
	app := filepath.Join(nodeDir, macOSAppName)
	if _, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "zerohour-qt")); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/open", "-a", app, "--args", "-datadir="+dataDir, "-server=1", "-choosedatadir=0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("launch Evolution: %w: %s", err, out)
	}
	return nil
}
