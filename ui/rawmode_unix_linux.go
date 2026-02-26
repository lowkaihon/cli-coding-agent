//go:build linux

package ui

import (
	"fmt"
	"os"
	"syscall"
)

// termios represents the Linux terminal I/O settings.
type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   byte
	Cc     [32]byte
	Ispeed uint32
	Ospeed uint32
}

const (
	icanon = 0x00000002
	echo   = 0x00000008
	vmin   = 6
	vtime  = 5
)

// ReadKeyContext reads a single byte from stdin, cancellable via the done channel.
// Uses select(2) with a 100ms timeout to poll for data.
func (rm *RawMode) ReadKeyContext(done <-chan struct{}) (byte, error) {
	buf := make([]byte, 1)
	fd := int(rm.fd)
	for {
		select {
		case <-done:
			return 0, ErrStopped
		default:
		}

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
			continue
		}

		_, err = os.Stdin.Read(buf)
		if err != nil {
			return 0, err
		}
		return buf[0], nil
	}
}
