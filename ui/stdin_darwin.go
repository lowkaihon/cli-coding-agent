//go:build darwin

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
	readFds.Bits[fd/32] |= 1 << (uint(fd) % 32)
	tv := syscall.Timeval{}
	err := syscall.Select(fd+1, &readFds, nil, nil, &tv)
	if err != nil {
		return false
	}
	// On Darwin, Select returns only an error. Check if the fd bit is still set.
	return readFds.Bits[fd/32]&(1<<(uint(fd)%32)) != 0
}
