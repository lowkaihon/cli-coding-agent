//go:build windows

package ui

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode    = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode    = kernel32.NewProc("SetConsoleMode")
	procGetStdHandle      = kernel32.NewProc("GetStdHandle")
	procReadConsoleInput      = kernel32.NewProc("ReadConsoleInputW")
	procGetNumberOfEvents     = kernel32.NewProc("GetNumberOfConsoleInputEvents")
	procWaitForSingleObject   = kernel32.NewProc("WaitForSingleObject")
)

const (
	enableLineInput       = 0x0002
	enableEchoInput       = 0x0004
	enableProcessedInput  = 0x0001
	stdInputHandle        = ^uintptr(0) - 10 + 1 // STD_INPUT_HANDLE = -10
	keyEventType          = 0x0001
	waitObject0           = 0x00000000
	waitTimeout           = 0x00000102
)

// ErrStopped is returned by ReadKeyContext when the done channel is closed.
var ErrStopped = errors.New("read stopped")

// inputRecord represents a Windows INPUT_RECORD for key events.
type inputRecord struct {
	EventType uint16
	_         uint16 // padding
	KeyEvent  keyEventRecord
}

type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

// RawMode manages Windows console raw mode state.
type RawMode struct {
	handle   syscall.Handle
	origMode uint32
}

// NewRawMode creates a new RawMode for the console stdin.
func NewRawMode() (*RawMode, error) {
	h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return nil, fmt.Errorf("get stdin handle: %w", err)
	}

	var mode uint32
	r, _, e := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return nil, fmt.Errorf("get console mode: %v", e)
	}

	return &RawMode{handle: h, origMode: mode}, nil
}

// Enable puts the console into raw mode (no line buffering, no echo).
func (rm *RawMode) Enable() error {
	raw := rm.origMode &^ (enableLineInput | enableEchoInput | enableProcessedInput)
	r, _, e := procSetConsoleMode.Call(uintptr(rm.handle), uintptr(raw))
	if r == 0 {
		return fmt.Errorf("set console mode: %v", e)
	}
	return nil
}

// Disable restores the original console mode.
func (rm *RawMode) Disable() error {
	r, _, e := procSetConsoleMode.Call(uintptr(rm.handle), uintptr(rm.origMode))
	if r == 0 {
		return fmt.Errorf("restore console mode: %v", e)
	}
	return nil
}

// ReadKeyContext reads a single key event from the console, but can be
// cancelled by closing the done channel. Uses WaitForSingleObject with a
// timeout to avoid blocking indefinitely.
func (rm *RawMode) ReadKeyContext(done <-chan struct{}) (byte, error) {
	for {
		// Check if we should stop
		select {
		case <-done:
			return 0, ErrStopped
		default:
		}

		// Wait for input with 100ms timeout
		ret, _, _ := procWaitForSingleObject.Call(uintptr(rm.handle), 100)
		if ret == waitTimeout {
			continue // no input yet, loop back and check done
		}
		if ret != waitObject0 {
			return 0, fmt.Errorf("wait for console input failed: %d", ret)
		}

		// Input is available — read it (non-blocking since we know data is ready)
		var rec inputRecord
		var numRead uint32
		r, _, e := procReadConsoleInput.Call(
			uintptr(rm.handle),
			uintptr(unsafe.Pointer(&rec)),
			1,
			uintptr(unsafe.Pointer(&numRead)),
		)
		if r == 0 {
			return 0, fmt.Errorf("read console input: %v", e)
		}
		if numRead == 0 {
			continue
		}
		if rec.EventType == keyEventType && rec.KeyEvent.KeyDown != 0 {
			ch := byte(rec.KeyEvent.UnicodeChar)
			if ch != 0 {
				return ch, nil
			}
			if rec.KeyEvent.VirtualKeyCode == 0x1B {
				return 0x1B, nil
			}
		}
	}
}
