// Package runner executes go subcommands with a timeout and captured output.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Result is the outcome of one subprocess.
type Result struct {
	Args     []string
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

// OK reports a clean, in-time run.
func (r *Result) OK() bool { return r.ExitCode == 0 && !r.TimedOut }

// Tail returns the last n lines of stderr, falling back to stdout.
func (r *Result) Tail(n int) string {
	src := string(r.Stderr)
	if strings.TrimSpace(src) == "" {
		src = string(r.Stdout)
	}
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Runner executes go commands in a fixed directory.
type Runner struct {
	// Dir is the working directory for every command.
	Dir string
	// Env replaces the process environment when non-nil.
	Env []string
	// Timeout bounds each command.
	Timeout time.Duration
	// Log receives the command line and its output. May be nil.
	Log io.Writer
}

// New returns a Runner rooted at dir.
func New(dir string, timeout time.Duration, log io.Writer) *Runner {
	return &Runner{Dir: dir, Timeout: timeout, Log: log}
}

// Go runs "go <args...>".
func (r *Runner) Go(ctx context.Context, args ...string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = r.Dir
	if r.Env != nil {
		cmd.Env = r.Env
	} else {
		cmd.Env = os.Environ()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	res := &Result{
		Args:     args,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: time.Since(start),
	}

	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
	} else if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			// The process could not be started at all.
			return nil, fmt.Errorf("run go %s: %w", strings.Join(args, " "), err)
		}
	}

	if r.Log != nil {
		fmt.Fprintf(r.Log, "\n$ go %s\n(dir=%s exit=%d timedOut=%v took=%s)\n",
			strings.Join(args, " "), r.Dir, res.ExitCode, res.TimedOut, res.Duration.Round(time.Millisecond))
		r.Log.Write(res.Stdout)
		r.Log.Write(res.Stderr)
	}
	return res, nil
}

// Build compiles every package.
func (r *Runner) Build(ctx context.Context) (*Result, error) {
	return r.Go(ctx, "build", "./...")
}

// Vet runs the vet suite.
func (r *Runner) Vet(ctx context.Context) (*Result, error) {
	return r.Go(ctx, "vet", "./...")
}

// Test runs the full test suite, optionally under the race detector.
func (r *Runner) Test(ctx context.Context, race bool) (*Result, error) {
	args := []string{"test", "./...", "-count=1"}
	if race {
		args = append(args, "-race")
	}
	return r.Go(ctx, args...)
}

// Bench runs one measurement round for the benchmarks matching pattern.
// Tests are skipped via -run '^$' so only benchmark time is measured.
func (r *Runner) Bench(ctx context.Context, pattern, benchtime string) (*Result, error) {
	return r.Go(ctx,
		"test", "./...",
		"-run", "^$",
		"-bench", pattern,
		"-benchmem",
		"-benchtime", benchtime,
		"-count=1",
	)
}
