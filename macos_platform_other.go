//go:build !darwin

package main

import "errors"

func stopMacOSNodeService() error { return errors.New("macOS only") }

func lockMacOSInstaller() (func(), error) {
	return nil, errors.New("macOS desktop mode is only available on macOS")
}
func checkMacOSExtractionSpace(archive, dataDir string) error {
	return errors.New("macOS desktop mode is only available on macOS")
}
