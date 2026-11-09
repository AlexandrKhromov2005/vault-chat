//go:build linux

package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	workerCPULimit  = "2:2"
	workerWaitDelay = time.Second
)

// Decode checks the image header, then runs the decoder with an empty
// environment, a two-second CPU limit and a bounded wall-clock deadline.
func (w Worker) Decode(ctx context.Context, data []byte) (Result, error) {
	width, height, err := expectedSize(data)
	if err != nil {
		return Result{}, err
	}
	if w.Timeout < 0 {
		return Result{}, fmt.Errorf("%w: negative timeout", ErrWorker)
	}
	timeout := w.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		return Result{}, fmt.Errorf("%w: prlimit is unavailable", ErrWorker)
	}
	args := []string{"--cpu=" + workerCPULimit, "--", w.Path}
	args = append(args, w.Args...)
	cmd := exec.CommandContext(ctx, prlimit, args...)
	cmd.Env = []string{}
	cmd.Stdin = bytes.NewReader(data)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = workerWaitDelay
	stdout := boundedWriter{limit: maxWorkerResponse}
	stderr := boundedWriter{limit: maxWorkerStderr}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrWorker, ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return Result{}, fmt.Errorf("%w: process output exceeds its budget", ErrWorker)
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			switch exit.ExitCode() {
			case 2:
				return Result{}, ErrInvalid
			case 3:
				return Result{}, ErrSkipped
			}
		}
		return Result{}, fmt.Errorf("%w: decoder exited without a valid response", ErrWorker)
	}
	return parseWorkerResponse(stdout.Bytes(), width, height)
}

var errOutputLimit = errors.New("preview: process output limit")

type boundedWriter struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if len(data) > remaining {
		_, _ = w.buffer.Write(data[:remaining])
		w.exceeded = true
		return 0, errOutputLimit
	}
	return w.buffer.Write(data)
}

func (w *boundedWriter) Bytes() []byte {
	return w.buffer.Bytes()
}
