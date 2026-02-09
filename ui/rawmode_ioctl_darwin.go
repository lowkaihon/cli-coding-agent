//go:build darwin

package ui

func tcgets() uintptr { return 0x40487413 } // TIOCGETA
func tcsets() uintptr { return 0x80487414 } // TIOCSETA
