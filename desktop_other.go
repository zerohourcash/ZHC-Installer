//go:build !windows

package main

import "fmt"

func windowsDesktopDirectory() (string, error) {
	return "", fmt.Errorf("Windows Desktop resolution is only available on Windows")
}
