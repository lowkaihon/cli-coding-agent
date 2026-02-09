//go:build linux

package ui

func tcgets() uintptr { return 0x5401 } // TCGETS
func tcsets() uintptr { return 0x5402 } // TCSETS
