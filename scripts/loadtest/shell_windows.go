//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// shell runs a -kill or -restart command through cmd, passing the line as
// it was written: Go's own quoting of arguments is not what cmd expects. A
// command gets a minute.
func shell(line string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd /C " + line}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
