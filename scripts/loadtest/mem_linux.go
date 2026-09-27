//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// statusKB reads a field in kB from /proc/<pid>/status.
func statusKB(pid int, field string) (uint64, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, field+":"); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
					return kb * 1024, true
				}
			}
		}
	}
	return 0, false
}

// peakMemory returns the largest resident set the process has had (VmHWM).
func peakMemory(pid int) (uint64, bool) { return statusKB(pid, "VmHWM") }

// usageNow returns the process's resident set and open file descriptors now.
func usageNow(pid int) (uint64, int, bool) {
	rss, ok := statusKB(pid, "VmRSS")
	if !ok {
		return 0, 0, false
	}
	fds, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	return rss, len(fds), true
}
