//go:build windows

package transport

import (
	"context"
	"io"
)

func runEvaluation(ctx context.Context, executable string, args []string) (evaluationResult, error) {
	var stderr boundedWriter
	p, err := startManaged(ctx, executable, args, false, &stderr)
	if err != nil {
		return evaluationResult{}, err
	}
	defer p.Close()
	stdoutDone := make(chan boundedOutput, 1)
	go func() { stdoutDone <- readBounded(p.Output, evaluateStdoutLimit) }()
	waitErr := p.Wait()
	stdout := <-stdoutDone
	if err := ctx.Err(); err != nil {
		_ = p.Close()
		return evaluationResult{}, err
	}
	if waitErr != nil {
		_ = p.Close()
		return evaluationResult{}, waitErr
	}
	return evaluationResult{stdout: stdout.data, exit: p.ExitCode(), tooBig: stdout.tooBig || stderr.tooBig}, nil
}

type boundedWriter struct {
	data   []byte
	tooBig bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	remaining := evaluateStderrLimit - len(w.data)
	if remaining > 0 {
		keep := min(remaining, len(p))
		w.data = append(w.data, p[:keep]...)
	}
	if len(p) > remaining {
		w.tooBig = true
	}
	return len(p), nil
}

var _ io.Writer = (*boundedWriter)(nil)
