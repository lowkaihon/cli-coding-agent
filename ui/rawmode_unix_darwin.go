//go:build darwin

package ui

import (
	"fmt"
	"os"
	"syscall"
)

// termios represents the Darwin terminal I/O settings.
type termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]byte
	Ispeed uint64
	Ospeed uint64
}

const (
	icanon = 0x00000100
	echo   = 0x00000008
	vmin   = 16
	vtime  = 17
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
		readFds.Bits[fd/32] |= 1 << (uint(fd) % 32)
		tv := syscall.Timeval{Usec: 100000} // 100ms
		err := syscall.Select(fd+1, &readFds, nil, nil, &tv)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return 0, fmt.Errorf("select: %w", err)
		}
		// On Darwin, Select returns only an error. Check if the fd bit is still set.
		if readFds.Bits[fd/32]&(1<<(uint(fd)%32)) == 0 {
			continue
		}

		_, err = os.Stdin.Read(buf)
		if err != nil {
			return 0, err
		}
		return buf[0], nil
	}
}
