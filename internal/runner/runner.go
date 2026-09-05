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

// capWriter retains at most limit bytes, then drops the rest.
type capWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			w.buf.Write(p[:remaining])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else {
		w.truncated = true
	}
	// Always report a full write: a short write would make exec fail the
	// command and would misreport a chatty test as a harness error.
	return len(p), nil
}

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
	if n < 0 {
		n = 0
	}
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

	setupProcessGroup(cmd)
	cmd.WaitDelay = 10 * time.Second

	const capBytes = 4 * 1024 * 1024 // 4 MB per stream
	stdout := &capWriter{limit: capBytes}
	stderr := &capWriter{limit: capBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err := cmd.Run()

	// Extract bytes and add truncation markers if needed
	stdoutBytes := stdout.buf.Bytes()
	if stdout.truncated {
		stdoutBytes = append(stdoutBytes, []byte("\n[output truncated at 4MB]\n")...)
	}
	stderrBytes := stderr.buf.Bytes()
	if stderr.truncated {
		stderrBytes = append(stderrBytes, []byte("\n[output truncated at 4MB]\n")...)
	}

	res := &Result{
		Args:     args,
		Stdout:   stdoutBytes,
		Stderr:   stderrBytes,
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

// BenchProfile runs benchmarks with CPU and memory profiling.
// pkg is the package pattern to test, e.g. "." or "./sub/dir" — never
// "./..." here, because the go tool refuses -cpuprofile/-memprofile when
// the pattern matches more than one package. cpuProfile and memProfile are
// the output file paths for the profiles. binaryPath is the path where the
// compiled test binary will be written.
func (r *Runner) BenchProfile(ctx context.Context, pkg, pattern, benchtime, cpuProfile, memProfile, binaryPath string) (*Result, error) {
	return r.Go(ctx,
		"test", pkg,
		"-run", "^$",
		"-bench", pattern,
		"-benchtime", benchtime,
		"-count=1",
		"-cpuprofile", cpuProfile,
		"-memprofile", memProfile,
		"-o", binaryPath,
	)
}
