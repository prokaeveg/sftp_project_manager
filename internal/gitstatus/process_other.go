//go:build !windows

package gitstatus

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
