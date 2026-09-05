package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/g4lb/autoresearch-go/internal/bench"
	"github.com/g4lb/autoresearch-go/internal/config"
	"github.com/g4lb/autoresearch-go/internal/discover"
	"github.com/g4lb/autoresearch-go/internal/freeze"
	"github.com/g4lb/autoresearch-go/internal/gitx"
	"github.com/g4lb/autoresearch-go/internal/state"
	"github.com/g4lb/autoresearch-go/internal/verdict"
	"gopkg.in/yaml.v3"
)

// --- test helpers -----------------------------------------------------
//
// internal/pipeline cannot import cmd/autoresearch-go's test helpers (they
// are unexported and live in package main), so the small slice of them this
// package needs — copying the demo fixture, running git, committing — is
// reimplemented here.

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// commit stages every change in dir and commits it.
func commit(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", msg)
}

// appendToFile appends content to the file at path.
func appendToFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", src, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read dir %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, s, d)
			continue
		}
		copyFile(t, s, d)
	}
}

// demoRepoSrc locates the shared testdata/demo fixture relative to this
// package, which lives at internal/pipeline.
func demoRepoSrc(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("testdata/demo fixture missing: %v", err)
	}
	return dir
}

// copyDemoRepo copies testdata/demo into a fresh temp directory, git-inits
// it with a repo-local identity, and commits it. It returns the new
// repository root.
func copyDemoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copyDir(t, demoRepoSrc(t), dir)

	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Gal Be")
	runGit(t, dir, "config", "user.email", "galevgi@gmail.com")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "initial commit")
	return dir
}

// evalEnv is everything one pipeline.Eval call needs, produced by setupRun.
type evalEnv struct {
	Root     string
	StateDir string
	Cfg      config.Config
	Base     *state.Baseline
	Log      *bytes.Buffer
}

// Options builds the pipeline.Options for the current state of env. Cfg is
// read fresh each call so a test can mutate env.Cfg (e.g. to narrow Scope)
// before evaluating.
func (e *evalEnv) Options() Options {
	return Options{
		Root:     e.Root,
		StateDir: e.StateDir,
		Cfg:      e.Cfg,
		Base:     e.Base,
		Log:      e.Log,
	}
}

// setupRun produces a repository that has been through init (a written
// .autoresearch/config.yaml, committed) and baseline (frozen tests, a
// recorded state.Baseline, and a pinned baseline worktree) — mirroring what
// a human running `autoresearch-go init && autoresearch-go baseline` would
// leave behind, using the internal packages directly since cmd/autoresearch-go's
// runInit/runBaseline live in package main and cannot be imported here.
func setupRun(t *testing.T) *evalEnv {
	t.Helper()
	return setupRunWithBenchmarks(t, []string{"BenchmarkCountWords"}, nil)
}

// setupRunWithBenchmarks is setupRun generalized to declare a specific
// benchmark set and, optionally, write extra files (relative path ->
// content) into the fixture before it is committed and frozen. Used by
// TestEvalToleratesFailedAllocsComparison, which needs a benchmark that
// makes zero allocations rather than the fixture's default one.
func setupRunWithBenchmarks(t *testing.T, benchmarks []string, extraFiles map[string]string) *evalEnv {
	t.Helper()
	root := copyDemoRepo(t)

	for rel, content := range extraFiles {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.Benchmarks = benchmarks
	// Short benchtime and few rounds keep the measuring scenarios well
	// under a minute. Count must be at least 4: benchmath's Mann-Whitney
	// test on n=3 vs n=3 samples cannot report p < 0.05 no matter how
	// clean the separation is (its minimum two-sided p-value at n=3 is
	// exactly 0.1) — the brief's suggested Count: 3 makes
	// TestEvalKeepsGenuineOptimization statistically unable to ever pass.
	// Count: 5 gives real headroom (min p ~= 0.008) against ordinary
	// measurement noise while still finishing in a few seconds.
	cfg.Benchtime = "50ms"
	cfg.Count = 5

	cfgBytes, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configPath := filepath.Join(root, config.Path)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, cfgBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, root, "autoresearch-go init")

	tag := "test"
	branch := "autoresearch-go/" + tag
	runGit(t, root, "checkout", "-q", "-b", branch)

	stateDir, err := state.StateDir(root, tag)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files, err := discover.TestFiles(root, cfg.Unfreeze)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := freeze.Snapshot(root, filepath.Join(stateDir, freeze.StoreDir), files)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := manifest.Save(filepath.Join(stateDir, freeze.ManifestPath)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	headCommit, err := gitx.HeadCommit(root)
	if err != nil {
		t.Fatal(err)
	}
	configHash, err := sha256File(configPath)
	if err != nil {
		t.Fatalf("hash config: %v", err)
	}

	base := &state.Baseline{
		Tag:           tag,
		Branch:        branch,
		Commit:        headCommit,
		MeasureCommit: headCommit,
		CreatedAt:     time.Now().UTC(),
		Benchmarks:    cfg.Benchmarks,
		Pattern:       state.BenchPattern(cfg.Benchmarks),
		ConfigSHA256:  configHash,
	}
	if err := base.Save(filepath.Join(stateDir, state.BaselineFile)); err != nil {
		t.Fatalf("save baseline: %v", err)
	}

	worktree := filepath.Join(stateDir, state.WorktreeName)
	if err := gitx.AddWorktree(root, worktree, headCommit); err != nil {
		t.Fatalf("pin baseline worktree: %v", err)
	}

	return &evalEnv{Root: root, StateDir: stateDir, Cfg: cfg, Base: base, Log: &bytes.Buffer{}}
}

// writeOptimizedWordCount replaces the fixture's deliberately quadratic
// CountWords with a strings.Builder + byte-loop + preallocated-map version
// that is both correct (passes the frozen tests) and substantially faster.
func writeOptimizedWordCount(t *testing.T, root string) {
	t.Helper()
	const src = `// Package demo is a fixture for autoresearch-go's own integration tests.
package demo

import "strings"

// CountWords returns how many times each lowercase word appears in s.
// Words are separated by whitespace; surrounding punctuation is stripped.
func CountWords(s string) map[string]int {
	counts := make(map[string]int, 64)
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			counts[b.String()]++
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			flush()
		}
		// Any other byte (punctuation) is dropped without flushing, so it
		// does not split the word — matching strings.Fields' contract that
		// punctuation is stripped, not treated as a separator.
	}
	flush()
	return counts
}
`
	if err := os.WriteFile(filepath.Join(root, "wordcount.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write optimized wordcount.go: %v", err)
	}
}

// writeBrokenWordCount replaces CountWords with an implementation that is
// fast but wrong: it always reports no words at all.
func writeBrokenWordCount(t *testing.T, root string) {
	t.Helper()
	const src = `// Package demo is a fixture for autoresearch-go's own integration tests.
package demo

// CountWords is deliberately broken: it always reports no words.
func CountWords(s string) map[string]int {
	return map[string]int{}
}
`
	if err := os.WriteFile(filepath.Join(root, "wordcount.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write broken wordcount.go: %v", err)
	}
}

// workTestSource is the frozen benchmark for the "work" fixture used by the
// advancing-measurement-baseline scenarios below (TestEvalDiscards... and
// TestEvalKeepsGenuine...). Its runtime is entirely controlled by the
// workSpins variable in work.go — an ordinary, in-scope source file the
// tests rewrite between experiments — rather than by any actual Go
// optimization idiom, so these tests get a large, deterministic,
// machine-independent speed difference instead of hoping a real code change
// measures faster on whatever hardware runs the suite.
const workTestSource = `package demo

import "testing"

// BenchmarkWork exists purely as a fixture for pipeline_test.go's
// advancing-baseline scenarios. Its cost is controlled entirely by
// workSpins in work.go, which the test rewrites to stand in for "the
// agent's optimization" between experiments.
func BenchmarkWork(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if Work() < 0 {
			b.Fatal("impossible")
		}
	}
}
`

// workFixtureFiles returns the extraFiles map for setupRunWithBenchmarks
// that pins BenchmarkWork's frozen test file and an initial work.go with
// the given spin count.
func workFixtureFiles(initialSpins int) map[string]string {
	return map[string]string{
		"work_test.go": workTestSource,
		"work.go":      workSource(initialSpins),
	}
}

// workSource renders work.go for the given spin count.
func workSource(spins int) string {
	return fmt.Sprintf(`package demo

// workSpins controls how much busy-work Work performs. Lowering it stands
// in for a genuine optimization in pipeline_test.go's advancing-baseline
// scenarios.
var workSpins = %d

// Work performs an amount of busy arithmetic proportional to workSpins.
func Work() int {
	sum := 0
	for i := 0; i < workSpins; i++ {
		sum += i %% 7
	}
	return sum
}
`, spins)
}

// writeWorkSpins rewrites work.go with a new spin count, leaving the rest
// of the fixture untouched.
func writeWorkSpins(t *testing.T, root string, spins int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte(workSource(spins)), 0o644); err != nil {
		t.Fatalf("write work.go (spins=%d): %v", spins, err)
	}
}

// addComment appends a no-op comment line to a Go source file — a change
// that alters nothing about what the file does, used to construct a "did
// this experiment actually help" regression case.
func addComment(t *testing.T, path, comment string) {
	t.Helper()
	appendToFile(t, path, "\n// "+comment+"\n")
}

// --- scenarios ----------------------------------------------------------

func TestEvalKeepsGenuineOptimization(t *testing.T) {
	if testing.Short() {
		t.Skip("measures real benchmarks; skipped in -short")
	}
	env := setupRun(t) // init + baseline, returns root, cfg, baseline
	// Replace the quadratic word builder with a strings.Builder version.
	writeOptimizedWordCount(t, env.Root)
	commit(t, env.Root, "use strings.Builder")

	res, deltas, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusKeep {
		t.Fatalf("status = %s (%s), want KEEP. deltas=%+v\nlog:\n%s", res.Status, res.Message, deltas, env.Log)
	}
	if res.Score >= 1 {
		t.Errorf("score = %v, want < 1", res.Score)
	}
}

func TestEvalFailsWhenTestsBreak(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real build/vet/test gates; skipped in -short")
	}
	env := setupRun(t)
	// Return an empty map: fast, but wrong.
	writeBrokenWordCount(t, env.Root)
	commit(t, env.Root, "return nothing")

	res, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusFail || res.Reason != verdict.ReasonTests {
		t.Fatalf("status = %s/%s, want FAIL/tests_failed\nlog:\n%s", res.Status, res.Reason, env.Log)
	}
}

func TestEvalRestoresWeakenedTests(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real build/vet/test gates; skipped in -short")
	}
	env := setupRun(t)
	// The agent both breaks the implementation AND deletes the test that
	// would catch it. Restore must resurrect the test and the run must FAIL.
	writeBrokenWordCount(t, env.Root)
	os.WriteFile(filepath.Join(env.Root, "wordcount_test.go"),
		[]byte("package demo\n"), 0o644)
	commit(t, env.Root, "break impl and gut tests")

	res, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusFail {
		t.Fatalf("status = %s, want FAIL: gutting the tests must not pass\nlog:\n%s", res.Status, env.Log)
	}
	got, _ := os.ReadFile(filepath.Join(env.Root, "wordcount_test.go"))
	if !strings.Contains(string(got), "TestCountWords") {
		t.Error("frozen test was not restored")
	}
}

func TestEvalRejectsNewTestFile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real correctness gates; skipped in -short")
	}
	env := setupRun(t)
	// The agent adds a brand-new benchmark file that did not exist at baseline.
	// Restore cannot erase it (it was never frozen) and the scope gate skips
	// every _test.go, so this check is the only thing standing in the way.
	os.WriteFile(filepath.Join(env.Root, "easy_test.go"), []byte(`package demo

import "testing"

func BenchmarkEasy(b *testing.B) {
	for i := 0; i < b.N; i++ {
	}
}
`), 0o644)
	commit(t, env.Root, "add an easier benchmark")

	res, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusFail || res.Reason != verdict.ReasonNewTestFile {
		t.Fatalf("status = %s/%s, want FAIL/new_test_file\nlog:\n%s", res.Status, res.Reason, env.Log)
	}
}

func TestEvalFailsOnSymlinkSwappedTestFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically needs elevation on Windows")
	}
	if testing.Short() {
		t.Skip("runs the real correctness gates; skipped in -short")
	}
	env := setupRun(t)

	// The agent mid-run: delete a frozen test file and put a symlink to some
	// other file in its place. Restore would otherwise write the frozen
	// test's content straight through the link.
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	const victimContent = "do not touch this file\n"
	if err := os.WriteFile(victim, []byte(victimContent), 0o644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(env.Root, "wordcount_test.go")
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, testFile); err != nil {
		t.Fatal(err)
	}
	commit(t, env.Root, "swap frozen test file for a symlink")

	res, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval returned a Go error %v, want a FAIL verdict instead", err)
	}
	if res.Status != verdict.StatusFail || res.Reason != verdict.ReasonSymlinkSwap {
		t.Fatalf("status = %s/%s, want FAIL/symlink_swap\nlog:\n%s", res.Status, res.Reason, env.Log)
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(got) != victimContent {
		t.Errorf("outside file content = %q, want unchanged %q", got, victimContent)
	}
}

func TestEvalFailsWhenBaselineWorktreeTampered(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real correctness gates; skipped in -short")
	}
	env := setupRun(t)

	// Simulate an agent (or an accidental clobber) editing the pinned
	// baseline worktree in place: check out a different commit inside it
	// so its HEAD no longer matches env.Base.Commit. copyDemoRepo's
	// "initial commit" is the parent of setupRun's "autoresearch-go init"
	// commit (the pinned baseline commit), so HEAD~1 inside the worktree
	// reaches it.
	worktreeDir := filepath.Join(env.StateDir, state.WorktreeName)
	runGit(t, worktreeDir, "checkout", "-q", "HEAD~1")

	writeOptimizedWordCount(t, env.Root)
	commit(t, env.Root, "use strings.Builder")

	res, meas, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusFail || res.Reason != verdict.ReasonBaselineTampered {
		t.Fatalf("status = %s/%s, want FAIL/baseline_tampered\nlog:\n%s", res.Status, res.Reason, env.Log)
	}
	if meas != nil {
		t.Errorf("Measurements = %+v, want nil: a tampered baseline must not be measured at all", meas)
	}
}

func TestEvalRejectsGoModEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real correctness gates; skipped in -short")
	}
	// The default scope is "./...", which matches root files, so go.mod must be
	// rejected by a rule of its own rather than by the scope patterns.
	env := setupRun(t)
	appendToFile(t, filepath.Join(env.Root, "go.mod"), "\n// touched\n")
	commit(t, env.Root, "touch go.mod")

	res, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusFail || res.Reason != verdict.ReasonScope {
		t.Fatalf("status = %s/%s, want FAIL/scope_violation\nlog:\n%s", res.Status, res.Reason, env.Log)
	}
}

func TestEvalRejectsOutOfScopeEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real correctness gates; skipped in -short")
	}
	env := setupRun(t)
	env.Cfg.Scope = []string{"./nowhere/..."}
	writeOptimizedWordCount(t, env.Root)
	commit(t, env.Root, "edit outside scope")

	res, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusFail || res.Reason != verdict.ReasonScope {
		t.Fatalf("status = %s/%s, want FAIL/scope_violation\nlog:\n%s", res.Status, res.Reason, env.Log)
	}
}

func TestEvalReportsAllocsAsHintNeverScored(t *testing.T) {
	if testing.Short() {
		t.Skip("measures real benchmarks; skipped in -short")
	}
	env := setupRun(t)
	// writeOptimizedWordCount drops the per-rune string concatenation that
	// made the original quadratic, which removes the bulk of its
	// allocations along with most of its time — exactly the combination
	// program.md's idea bank expects an agent to look for.
	writeOptimizedWordCount(t, env.Root)
	commit(t, env.Root, "use strings.Builder")

	res, meas, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if res.Status != verdict.StatusKeep {
		t.Fatalf("status = %s (%s), want KEEP\nlog:\n%s", res.Status, res.Message, env.Log)
	}
	if meas == nil {
		t.Fatal("Measurements = nil, want a populated result for a passing candidate")
	}
	if len(meas.Allocs) == 0 {
		t.Fatal("Allocs deltas empty, want the allocs/op comparison to have run")
	}
	for _, a := range meas.Allocs {
		if a.PctChange >= 0 {
			t.Errorf("%s allocs/op PctChange = %+.1f%%, want negative (fewer allocations)", a.Name, a.PctChange)
		}
	}

	// The verdict's score must come from Time alone. Recomputing the
	// geomean from meas.Time in isolation must reproduce res.Score exactly
	// — if Allocs ever leaked into scoring, this would drift even though
	// Allocs happens to point the same direction as Time here.
	wantScore, err := bench.GeoMean(meas.Time)
	if err != nil {
		t.Fatalf("GeoMean(meas.Time): %v", err)
	}
	if res.Score != wantScore {
		t.Errorf("res.Score = %v, want %v (geomean of Time deltas alone) — allocs must never feed the score",
			res.Score, wantScore)
	}
}

// TestEvalToleratesFailedAllocsComparison exercises Eval's handling of a
// failed allocs/op comparison directly, per the fix round's requirement: a
// missing hint must never turn into a discarded experiment. BenchmarkNoAlloc
// makes zero allocations on both baseline and candidate by construction, so
// every measured allocs/op value is exactly 0. That trips
// bench.Compare's "baseline median is zero, cannot form a ratio" guard,
// which is the cleanest deterministic way to force the comparison to fail
// without needing to fake or intercept internal/bench itself.
func TestEvalToleratesFailedAllocsComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("measures real benchmarks; skipped in -short")
	}
	const noAllocSrc = `package demo

import "testing"

// BenchmarkNoAlloc makes zero allocations by construction: it exists
// solely to trip bench.Compare's "baseline median is zero" guard for
// allocs/op, while still taking measurable wall-clock time so the time
// comparison that actually decides the verdict has real numbers to work
// with.
func BenchmarkNoAlloc(b *testing.B) {
	sum := 0
	for i := 0; i < b.N; i++ {
		sum += i
	}
	if sum < 0 {
		b.Fatal("impossible")
	}
}
`
	env := setupRunWithBenchmarks(t, []string{"BenchmarkNoAlloc"}, map[string]string{
		"noalloc_test.go": noAllocSrc,
	})
	// No source change needed at all: setupRunWithBenchmarks already
	// committed and pinned the baseline at a tree containing BenchmarkNoAlloc,
	// so Eval can run directly against it — the point here is only to reach
	// measurement with a benchmark whose allocs/op is always 0.
	res, meas, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}

	// The gate chain and measurement must have completed normally — a
	// missing allocs/op hint is not a reason to fail or crash the
	// experiment. res.Reason must be one of the three post-measurement
	// reasons, not any gate-failure reason.
	switch res.Reason {
	case verdict.ReasonImproved, verdict.ReasonNoImprovement, verdict.ReasonGuardRegression:
		// expected: a normal, scored outcome.
	default:
		t.Fatalf("status/reason = %s/%s, want a normal scored outcome (improved, "+
			"no_significant_improvement, or guard_regression), not a gate failure\nlog:\n%s",
			res.Status, res.Reason, env.Log)
	}

	if meas == nil {
		t.Fatal("Measurements = nil, want a populated result for a passing candidate")
	}
	if meas.Allocs != nil {
		t.Errorf("Measurements.Allocs = %+v, want nil: the comparison should have failed and been "+
			"swallowed, not silently produced (possibly bogus) deltas", meas.Allocs)
	}
	if len(meas.Time) == 0 {
		t.Error("Measurements.Time is empty, want the time comparison (which does not fail here) to have run")
	}
	if !strings.Contains(env.Log.String(), "allocs/op comparison unavailable") {
		t.Errorf("log = %q, want a note that the allocs/op comparison was skipped", env.Log.String())
	}
}

// TestEvalDiscardsNoOpAfterPriorImprovementKept is the regression test for
// the advancing-measurement-baseline fix: it is the test that would have
// caught the original flaw.
//
// Before the fix, every eval measured the candidate against the run's
// ORIGINAL commit for the life of the run. Once one real improvement was
// kept, every LATER experiment — however useless — was still being compared
// to that same stale, slower original state. A change that did nothing at
// all (here: a comment) would still look like a large improvement relative
// to the original baseline, so verdict.Decide's "score < 1 and a
// significant improvement" test was satisfied by an earlier, already-banked
// win rather than by anything the second experiment itself did — and it
// wrongly returned KEEP.
func TestEvalDiscardsNoOpAfterPriorImprovementKept(t *testing.T) {
	if testing.Short() {
		t.Skip("measures real benchmarks; skipped in -short")
	}
	env := setupRunWithBenchmarks(t, []string{"BenchmarkWork"}, workFixtureFiles(3000000))

	// Experiment 1: a genuine, large improvement. Must be kept, and the
	// measurement baseline must advance to this commit.
	writeWorkSpins(t, env.Root, 500000)
	commit(t, env.Root, "cut workSpins ~6x")
	res1, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval (experiment 1): %v", err)
	}
	if res1.Status != verdict.StatusKeep {
		t.Fatalf("experiment 1 status = %s (%s), want KEEP\nlog:\n%s", res1.Status, res1.Message, env.Log)
	}
	kept1Commit, err := gitx.HeadCommit(env.Root)
	if err != nil {
		t.Fatal(err)
	}
	if env.Base.MeasureCommit != kept1Commit {
		t.Fatalf("MeasureCommit = %s after experiment 1's KEEP, want it advanced to the kept commit %s",
			env.Base.MeasureCommit, kept1Commit)
	}
	if env.Base.Commit == kept1Commit {
		t.Fatalf("frozen Commit anchor changed to %s — it must never advance", kept1Commit)
	}

	// Experiment 2: a no-op. workSpins is untouched; only a comment is
	// added. Measured against the just-kept 500,000-spin state (the fix),
	// this is statistically indistinguishable from it and must DISCARD.
	// Measured against the original 3,000,000-spin state (the bug), it
	// would still look like a ~6x win and wrongly KEEP.
	env.Log = &bytes.Buffer{}
	addComment(t, filepath.Join(env.Root, "work.go"), "no-op: just a comment")
	commit(t, env.Root, "add a comment, nothing else")

	res2, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval (experiment 2): %v", err)
	}
	if res2.Status != verdict.StatusDiscard {
		t.Fatalf("experiment 2 (a no-op change made right after a prior KEEP) status = %s (%s), want DISCARD — "+
			"this is the exact flaw the advancing measurement baseline fixes: a no-op must not coast to "+
			"KEEP on the strength of an earlier, already-banked improvement\nlog:\n%s",
			res2.Status, res2.Message, env.Log)
	}
}

// TestEvalKeepsGenuineSecondImprovementOnTopOfFirst proves the fix does not
// overcorrect: a SECOND experiment that is a real improvement over the
// first (not merely over the run's original commit) must still KEEP, now
// measured against the first experiment's kept state rather than the
// original baseline.
func TestEvalKeepsGenuineSecondImprovementOnTopOfFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("measures real benchmarks; skipped in -short")
	}
	env := setupRunWithBenchmarks(t, []string{"BenchmarkWork"}, workFixtureFiles(3000000))

	writeWorkSpins(t, env.Root, 500000)
	commit(t, env.Root, "cut workSpins ~6x")
	res1, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval (experiment 1): %v", err)
	}
	if res1.Status != verdict.StatusKeep {
		t.Fatalf("experiment 1 status = %s (%s), want KEEP\nlog:\n%s", res1.Status, res1.Message, env.Log)
	}

	// Experiment 2: a further, genuine improvement on top of the first.
	env.Log = &bytes.Buffer{}
	writeWorkSpins(t, env.Root, 80000)
	commit(t, env.Root, "cut workSpins another ~6x")

	res2, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval (experiment 2): %v", err)
	}
	if res2.Status != verdict.StatusKeep {
		t.Fatalf("experiment 2 (a genuine further improvement) status = %s (%s), want KEEP\nlog:\n%s",
			res2.Status, res2.Message, env.Log)
	}
	if res2.Score >= 1 {
		t.Errorf("experiment 2 score = %v, want < 1", res2.Score)
	}
}

// TestEvalKeepsFrozenTestsAtOriginalCommitAfterBaselineAdvances proves that
// advancing MeasureCommit does not also "un-freeze" the success criteria:
// the frozen `_test.go` manifest and store, written once at `baseline`
// against the ORIGINAL commit, must still be what every later eval restores
// from and enforces, even after a KEEP has moved MeasureCommit well past
// that original commit.
func TestEvalKeepsFrozenTestsAtOriginalCommitAfterBaselineAdvances(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real correctness gates; skipped in -short")
	}
	env := setupRun(t)

	// Experiment 1: a genuine improvement, kept — advances MeasureCommit
	// away from the frozen Commit anchor.
	writeOptimizedWordCount(t, env.Root)
	commit(t, env.Root, "use strings.Builder")
	res1, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval (experiment 1): %v", err)
	}
	if res1.Status != verdict.StatusKeep {
		t.Fatalf("experiment 1 status = %s (%s), want KEEP\nlog:\n%s", res1.Status, res1.Message, env.Log)
	}
	frozenAnchor := env.Base.Commit
	if env.Base.MeasureCommit == frozenAnchor {
		t.Fatal("MeasureCommit did not advance after a KEEP")
	}

	// Experiment 2: break the implementation AND gut the frozen test, as in
	// TestEvalRestoresWeakenedTests. If advancing MeasureCommit had also
	// un-frozen the success criteria, the gutted test would stick and this
	// would wrongly pass instead of failing.
	env.Log = &bytes.Buffer{}
	writeBrokenWordCount(t, env.Root)
	if err := os.WriteFile(filepath.Join(env.Root, "wordcount_test.go"), []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, env.Root, "break impl and gut tests")

	res2, _, err := Eval(context.Background(), env.Options())
	if err != nil {
		t.Fatalf("Eval (experiment 2): %v", err)
	}
	if res2.Status != verdict.StatusFail {
		t.Fatalf("experiment 2 status = %s, want FAIL: the frozen test must still be enforced "+
			"even after the measurement baseline advanced\nlog:\n%s", res2.Status, env.Log)
	}
	got, err := os.ReadFile(filepath.Join(env.Root, "wordcount_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "TestCountWords") {
		t.Error("frozen test was not restored after the measurement baseline had advanced")
	}
	if env.Base.Commit != frozenAnchor {
		t.Fatalf("frozen Commit anchor changed from %s to %s — it must never advance", frozenAnchor, env.Base.Commit)
	}
}
