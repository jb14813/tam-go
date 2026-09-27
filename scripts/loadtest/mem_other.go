//go:build !windows && !linux

package main

// peakMemory is not measured on this system.
func peakMemory(pid int) (uint64, bool) { return 0, false }

// usageNow is not measured on this system.
func usageNow(pid int) (uint64, int, bool) { return 0, 0, false }
