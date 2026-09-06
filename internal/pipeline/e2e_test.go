package pipeline

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autoresearch-go/internal/config"
	"gopkg.in/yaml.v3"
)

// This file is the one test in the repository that drives the real,
// compiled `autoresearch-go` binary through the process boundary rather
// than calling package functions directly. Every other test — in this
// package and in cmd/autoresearch-go — either calls pipeline.Eval directly
// or calls cmd/autoresearch-go's unexported runInit/runBaseline/runEval/
// runReport in-process. Neither catches a wiring problem between the CLI
// layer and the packages it assembles (a flag mis-plumbed, a wrong exit
// code returned to the shell, output that looks right in-process but not
// through a real os/exec pipe). This test is what would catch that: it
// runs the exact sequence a human, or an unattended agent following
// program.md, actually types.

// buildBinary compiles cmd/autoresearch-go once into a temp directory and
// returns the path to the resulting binary. Building once and reusing the
// binary across every subcommand call below is much cheaper than paying
// `go run`'s build cost on each of the five invocations this test makes.
func buildBinary(t *testing.T) string {
	t.Helper()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "autoresearch-go")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/autoresearch-go")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/autoresearch-go: %v\n%s", err, out)
	}
	return bin
}

// runCLI executes the built binary with args and returns its combined
// stdout+stderr and exit code. A non-exec.ExitError failure (the binary
// itself could not be started) fails the test immediately, since that is
// never an expected outcome of any step below.
func runCLI(t *testing.T, bin string, args ...string) (output string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err == nil {
		return buf.String(), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return buf.String(), ee.ExitCode()
	}
	t.Fatalf("run %s %v: %v\n%s", bin, args, err, buf.String())
	return "", -1
}

// shrinkConfigForSpeed lowers count and benchtime so the measurement phase
// finishes in a few seconds instead of the ~20s the generated defaults
// (count: 10, benchtime: 1s) would take. Count must stay >= config's
// significance floor of 4, and — because this journey asserts a KEEP — well
// clear of it; see the comment on the assignment below.
func shrinkConfigForSpeed(t *testing.T, configPath string) {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	// 8 rounds, not 5. The journey asserts that a large, real speedup comes
	// back as KEEP, and KEEP needs statistical significance -- so `count`
	// has to leave the test room to be significant, not just fast.
	//
	// At 5 v 5 the smallest two-sided p a rank test can produce is 2/C(10,5)
	// = 0.0079, and reaching anything under alpha=0.05 requires almost
	// perfect separation of all ten samples. A few overlapping pairs on a
	// loaded CI runner push p past 0.05 and the journey fails on a genuine
	// 64% speedup. At 8 v 8 the floor drops to 2/C(16,8) = 0.00016, which
	// tolerates real overlap. It also clears the harness's own "need >= 6
	// samples for confidence interval" warning, which was already telling us
	// 5 was underpowered.
	//
	// The cost is 3 extra rounds per side at 50ms: fractions of a second
	// against a journey that shells out to the real toolchain.
	cfg.Count = 8
	cfg.Benchtime = "50ms"
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, b, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestEndToEndUserJourney runs the entire documented user journey against
// a throwaway copy of testdata/demo, through the real compiled binary:
// init, commit init's output, doctor, baseline, apply a real optimization,
// eval (must KEEP), report (must show exactly one kept experiment).
func TestEndToEndUserJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the real toolchain (build/vet/test/bench); skipped in -short")
	}

	bin := buildBinary(t)
	dir := copyDemoRepo(t)

	// 1. init: discover the benchmark, write config.yaml + program.md +
	// .gitignore.
	out, code := runCLI(t, bin, "init", "-C", dir)
	if code != 0 {
		t.Fatalf("init = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "BenchmarkCountWords") {
		t.Errorf("init output = %q, want it to report the discovered benchmark", out)
	}

	// Shrink count/benchtime for test speed before committing init's
	// output, so the one commit below captures the config this run
	// actually measures with.
	configPath := filepath.Join(dir, config.Path)
	shrinkConfigForSpeed(t, configPath)

	// 2. Commit init's output. `baseline` refuses a dirty working tree, and
	// this is exactly what init's own summary tells a human to do next —
	// skipping it is the single most common way to make a copy-pasted
	// quick start fail on the very next line.
	commit(t, dir, "autoresearch-go init")

	// doctor is informational only (it always exits 0) but sits between
	// init and baseline in the documented quick start.
	if out, code := runCLI(t, bin, "doctor", "-C", dir); code != 0 {
		t.Fatalf("doctor = %d, want 0\n%s", code, out)
	}

	// 3. baseline: freeze tests, pin the baseline commit.
	out, code = runCLI(t, bin, "baseline", "-C", dir, "-tag", "e2e")
	if code != 0 {
		t.Fatalf("baseline = %d, want 0\n%s", code, out)
	}

	// 4. Apply a real optimization — the same strings.Builder rewrite this
	// package's other Eval tests use — and commit it, exactly as an agent
	// following program.md would.
	writeOptimizedWordCount(t, dir)
	commit(t, dir, "use strings.Builder")

	// 5. eval must return KEEP (exit 0).
	out, code = runCLI(t, bin, "eval", "-C", dir, "-desc", "use strings.Builder")
	if code != 0 {
		t.Fatalf("eval = %d, want 0 (KEEP)\n%s", code, out)
	}
	if !strings.Contains(out, "VERDICT: KEEP") {
		t.Errorf("eval output = %q, want a VERDICT: KEEP line", out)
	}

	// 6. report must show exactly one kept experiment and nothing else.
	out, code = runCLI(t, bin, "report", "-C", dir)
	if code != 0 {
		t.Fatalf("report = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "1 total experiments") {
		t.Errorf("report output = %q, want 1 total experiment", out)
	}
	if !strings.Contains(out, "kept: 1") {
		t.Errorf("report output = %q, want kept: 1", out)
	}
	if !strings.Contains(out, "discarded: 0") ||
		!strings.Contains(out, "failed: 0") ||
		!strings.Contains(out, "crashed: 0") {
		t.Errorf("report output = %q, want every other status at 0", out)
	}
}
