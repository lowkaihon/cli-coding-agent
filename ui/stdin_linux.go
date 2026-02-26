//go:build linux

package ui

import (
	"os"
	"syscall"
)

// StdinHasData returns true if stdin has data ready to read without blocking.
// Uses select(2) with a zero timeout.
func StdinHasData() bool {
	fd := int(os.Stdin.Fd())
	var readFds syscall.FdSet
	readFds.Bits[fd/64] |= 1 << (uint(fd) % 64)
	tv := syscall.Timeval{}
	n, err := syscall.Select(fd+1, &readFds, nil, nil, &tv)
	if err != nil {
		return false
	}
	return n > 0
}
