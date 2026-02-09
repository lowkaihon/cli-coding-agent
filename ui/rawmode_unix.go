//go:build !windows

package ui

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// termios represents the Unix terminal I/O settings.
type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Cc     [20]byte
	Ispeed uint32
	Ospeed uint32
}

const (
	icanon = 0x00000002
	echo   = 0x00000008
	// termios ioctl constants differ by platform but TCGETS/TCSETS
	// are handled via syscall.SYS_IOCTL on Linux. On macOS/Darwin
	// the values are different, so we use the generic approach.
	vmin  = 6
	vtime = 5
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

// ReadKeyContext reads a single byte from stdin, but can be cancelled by
// closing the done channel. Uses select(2) with a timeout to avoid blocking.
func (rm *RawMode) ReadKeyContext(done <-chan struct{}) (byte, error) {
	buf := make([]byte, 1)
	fd := int(rm.fd)
	for {
		select {
		case <-done:
			return 0, ErrStopped
		default:
		}

		// Use select(2) with 100ms timeout to check if stdin is readable
		var readFds syscall.FdSet
		readFds.Bits[fd/64] |= 1 << (uint(fd) % 64)
		tv := syscall.Timeval{Usec: 100000} // 100ms
		n, err := syscall.Select(fd+1, &readFds, nil, nil, &tv)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return 0, fmt.Errorf("select: %w", err)
		}
		if n == 0 {
			continue // timeout, check done and try again
		}

		_, err = os.Stdin.Read(buf)
		if err != nil {
			return 0, err
		}
		return buf[0], nil
	}
}
