package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

func windowsDesktopDirectory() (string, error) {
	// FOLDERID_Desktop resolves the current user's redirected/OneDrive Desktop.
	id := syscall.GUID{Data1: 0xB4BFCC3A, Data2: 0xDB2C, Data3: 0x424C, Data4: [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}
	shell := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetKnownFolderPath")
	free := syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")
	if err := shell.Find(); err != nil {
		return "", err
	}
	if err := free.Find(); err != nil {
		return "", err
	}
	var ptr *uint16
	// KF_FLAG_DONT_VERIFY also returns the configured path when Desktop is missing.
	result, _, _ := shell.Call(uintptr(unsafe.Pointer(&id)), 0x4000, 0, uintptr(unsafe.Pointer(&ptr)))
	if ptr != nil {
		defer free.Call(uintptr(unsafe.Pointer(ptr)))
	}
	if result != 0 {
		return "", fmt.Errorf("SHGetKnownFolderPath failed: HRESULT 0x%08x", uint32(result))
	}
	if ptr == nil {
		return "", fmt.Errorf("Windows returned an empty Desktop path")
	}
	var path []uint16
	for i := 0; i < 32768; i++ {
		c := *(*uint16)(unsafe.Add(unsafe.Pointer(ptr), i*2))
		if c == 0 {
			return syscall.UTF16ToString(path), nil
		}
		path = append(path, c)
	}
	return "", fmt.Errorf("Windows Desktop path exceeds supported length")
}
