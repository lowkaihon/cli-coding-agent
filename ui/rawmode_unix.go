//go:build !windows

package ui

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ioctlTermios performs a termios ioctl syscall.
func ioctlTermios(fd, req uintptr, t *termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// RawMode manages Unix terminal raw mode state.
type RawMode struct {
	fd       uintptr
	origTerm termios
}

// NewRawMode creates a new RawMode for stdin.
func NewRawMode() (*RawMode, error) {
	fd := os.Stdin.Fd()
	var orig termios
	if err := ioctlTermios(fd, tcgets(), &orig); err != nil {
		return nil, fmt.Errorf("get termios: %w", err)
	}
	return &RawMode{fd: fd, origTerm: orig}, nil
}

// Enable puts the terminal into raw mode (no canonical mode, no echo).
func (rm *RawMode) Enable() error {
	raw := rm.origTerm
	raw.Lflag &^= icanon | echo
	raw.Cc[vmin] = 1
	raw.Cc[vtime] = 0
	if err := ioctlTermios(rm.fd, tcsets(), &raw); err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	return nil
}

// Disable restores the original terminal mode.
func (rm *RawMode) Disable() error {
	if err := ioctlTermios(rm.fd, tcsets(), &rm.origTerm); err != nil {
		return fmt.Errorf("restore termios: %w", err)
	}
	return nil
}

// ErrStopped is returned by ReadKeyContext when the done channel is closed.
var ErrStopped = errors.New("read stopped")
