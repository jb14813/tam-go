//go:build !windows && !linux

package main

// peakMemory is not measured on this system.
func peakMemory(pid int) (uint64, bool) { return 0, false }
