//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

// StdinHasData returns true if there are pending input events in the
// Windows console input buffer. This detects pasted multi-line input
// that hasn't been consumed by ReadFile yet.
func StdinHasData() bool {
	h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return false
	}
	var count uint32
	r, _, _ := procGetNumberOfEvents.Call(uintptr(h), uintptr(unsafe.Pointer(&count)))
	if r == 0 {
		return false
	}
	return count > 0
}
