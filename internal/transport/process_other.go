//go:build !windows

package transport

import (
	"context"
	"io"
	"os/exec"
	"sync"
)

func hideWindow(command *exec.Cmd) {}
func nullConfig() string           { return "/dev/null" }
func supportsConfigProxy() bool    { return false }

// managedProcess is the portable single-process fallback. Config proxies are
// rejected on these builds because process-tree cancellation is unavailable.
type managedProcess struct {
	Input     io.WriteCloser
	Output    io.ReadCloser
	Done      <-chan struct{}
	cmd       *exec.Cmd
	done      chan struct{}
	waitErr   error
	closeOnce sync.Once
}

func (p *managedProcess) PID() uint32 {
	if p.cmd.Process == nil {
		return 0
	}
	return uint32(p.cmd.Process.Pid)
}
func (p *managedProcess) Wait() error { <-p.done; return p.waitErr }
func (p *managedProcess) Close() error {
	p.closeOnce.Do(func() {
		if p.Input != nil {
			_ = p.Input.Close()
		}
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = p.Wait()
		if p.Output != nil {
			_ = p.Output.Close()
		}
	})
	return p.waitErr
}

func startManaged(ctx context.Context, executable string, args []string, pipeInput bool, stderr io.Writer) (*managedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	hideWindow(cmd)
	cmd.Env = append(cmd.Environ(), "SSH_ASKPASS_REQUIRE=never")
	if stderr != nil {
		cmd.Stderr = stderr
	}
	var input io.WriteCloser
	var err error
	if pipeInput {
		input, err = cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		if input != nil {
			_ = input.Close()
		}
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if input != nil {
			_ = input.Close()
		}
		_ = output.Close()
		return nil, err
	}
	p := &managedProcess{Input: input, Output: output, cmd: cmd, done: make(chan struct{})}
	p.Done = p.done
	go func() { p.waitErr = cmd.Wait(); close(p.done) }()
	return p, nil
}
