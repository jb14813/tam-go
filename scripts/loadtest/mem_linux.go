//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// peakMemory returns the largest resident set the process has had (VmHWM).
func peakMemory(pid int) (uint64, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
					return kb * 1024, true
				}
			}
		}
	}
	return 0, false
}
