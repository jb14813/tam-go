//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// shell runs a -kill or -restart command through sh. A command gets a
// minute.
func shell(line string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
