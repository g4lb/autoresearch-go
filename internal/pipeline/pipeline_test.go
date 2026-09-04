package pipeline

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	root := copyDemoRepo(t)

	cfg := config.Default()
	cfg.Benchmarks = []string{"BenchmarkCountWords"}
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
		Tag:          tag,
		Branch:       branch,
		Commit:       headCommit,
		CreatedAt:    time.Now().UTC(),
		Benchmarks:   cfg.Benchmarks,
		Pattern:      state.BenchPattern(cfg.Benchmarks),
		ConfigSHA256: configHash,
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
