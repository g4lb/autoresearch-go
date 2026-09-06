package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g4lb/autor3search-go/internal/config"
	"github.com/g4lb/autor3search-go/internal/freeze"
	"github.com/g4lb/autor3search-go/internal/gitx"
	"github.com/g4lb/autor3search-go/internal/results"
	"github.com/g4lb/autor3search-go/internal/state"
)

func TestBaselineCreatesBranchAndFreezesTests(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	br, _ := gitx.CurrentBranch(dir)
	if br != "autor3search-go/sep4" {
		t.Errorf("branch = %q, want autor3search-go/sep4", br)
	}
	stateDir, err := state.StateDir(dir, "sep4")
	if err != nil {
		t.Fatal(err)
	}
	b, err := state.LoadBaseline(filepath.Join(stateDir, state.BaselineFile))
	if err != nil {
		t.Fatalf("baseline.json: %v", err)
	}
	if b.Pattern != "^(BenchmarkCountWords)$" {
		t.Errorf("Pattern = %q", b.Pattern)
	}
	if b.MeasureCommit != b.Commit {
		t.Errorf("MeasureCommit = %q, want it to start equal to Commit %q", b.MeasureCommit, b.Commit)
	}
	m, err := freeze.LoadManifest(filepath.Join(stateDir, freeze.ManifestPath))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if _, ok := m.Files["wordcount_test.go"]; !ok {
		t.Errorf("manifest = %v, want wordcount_test.go frozen", m.Files)
	}
}

func TestBaselineWritesNothingIntoTheRepo(t *testing.T) {
	// Every artifact the metric depends on must live outside the repository.
	// An in-repo store, manifest, baseline record or worktree would be
	// silently writable by the very agent they constrain.
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d", code)
	}
	for _, rel := range []string{
		".autor3search/frozen",
		".autor3search/baseline.json",
		".autor3search/worktrees",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exists inside the repository; harness state must be out of tree", rel)
		}
	}
}

func TestBaselineRefusesExistingBranch(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	runBaseline([]string{"-C", dir, "-tag", "sep4"})
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code == exitOK {
		t.Fatal("reused an existing run tag, want refusal")
	}
}

func TestBaselineRefusesDirtyTree(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	os.WriteFile(filepath.Join(dir, "scratch.go"), []byte("package demo\n"), 0o644)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep5"}); code == exitOK {
		t.Fatal("accepted a dirty tree, want refusal")
	}
}

func TestBaselineRefusesToOverwriteResultsLog(t *testing.T) {
	// results.tsv is the sole, durable record of a previous unattended run.
	// A second baseline must not silently truncate it.
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	resultsPath := filepath.Join(dir, "results.tsv")
	if err := results.Append(resultsPath, results.Row{
		Commit: "abc1234", Score: 1.1, BestBenchDelta: 5, AllocsDelta: -2, Status: "keep", Description: "prior experiment",
	}); err != nil {
		t.Fatalf("seed results.tsv: %v", err)
	}
	before, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}

	if code := runBaseline([]string{"-C", dir, "-tag", "sep5"}); code == exitOK {
		t.Fatal("baseline overwrote a results.tsv holding a previous run's rows, want refusal")
	}

	after, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("results.tsv changed despite refusal:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestBaselineForceOverwritesResultsLog(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	if code := runBaseline([]string{"-C", dir, "-tag", "sep4"}); code != exitOK {
		t.Fatalf("runBaseline = %d, want %d", code, exitOK)
	}

	resultsPath := filepath.Join(dir, "results.tsv")
	if err := results.Append(resultsPath, results.Row{
		Commit: "abc1234", Score: 1.1, BestBenchDelta: 5, AllocsDelta: -2, Status: "keep", Description: "prior experiment",
	}); err != nil {
		t.Fatalf("seed results.tsv: %v", err)
	}

	if code := runBaseline([]string{"-C", dir, "-tag", "sep5", "-force"}); code != exitOK {
		t.Fatalf("runBaseline -force = %d, want %d", code, exitOK)
	}

	got, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != results.Header+"\n" {
		t.Errorf("results.tsv = %q, want just the header after -force", got)
	}
}

func TestBaselineRollsBackBranchOnFailureAfterCreation(t *testing.T) {
	// A failure after the run branch is created must not permanently burn
	// the -tag: it must leave the repository back on its original branch
	// with the (never-finished) run branch deleted, so a retry can succeed.
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	original, err := gitx.CurrentBranch(dir)
	if err != nil {
		t.Fatal(err)
	}

	stateDir, err := state.StateDir(dir, "sep6")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Occupy the frozen store's path with a plain file. freeze.Snapshot's
	// os.MkdirAll then fails deterministically and portably (a name
	// collision, not a permission check) once baseline reaches it — which
	// is after the run branch has already been created.
	storeDir := filepath.Join(stateDir, freeze.StoreDir)
	if err := os.WriteFile(storeDir, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runBaseline([]string{"-C", dir, "-tag", "sep6"}); code == exitOK {
		t.Fatal("runBaseline succeeded despite an occupied frozen-store path, want failure")
	}

	if br, err := gitx.CurrentBranch(dir); err != nil {
		t.Fatal(err)
	} else if br != original {
		t.Errorf("branch = %q after failed baseline, want back on %q", br, original)
	}
	if exists, err := gitx.BranchExists(dir, "autor3search-go/sep6"); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Error("autor3search-go/sep6 still exists after a failed baseline, want it deleted")
	}
}

func TestBaselineRefusesUnknownBenchmark(t *testing.T) {
	// A typo'd benchmark name in config.yaml must be caught here, before the
	// run branch exists — not left for eval's measure.Run to discover hours
	// later with "no benchmarks matched", by which point nobody is watching.
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	configPath := filepath.Join(dir, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Benchmarks = []string{"BenchmarkDoesNotExist"}
	if err := os.WriteFile(configPath, renderConfig(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "config names an unknown benchmark")

	var code int
	stderr := captureStderr(t, func() {
		code = runBaseline([]string{"-C", dir, "-tag", "sep7"})
	})
	if code == exitOK {
		t.Fatal("runBaseline accepted an unknown benchmark, want refusal")
	}
	if !strings.Contains(stderr, "BenchmarkDoesNotExist") {
		t.Errorf("stderr = %q, want it to name the unknown benchmark", stderr)
	}

	if exists, err := gitx.BranchExists(dir, "autor3search-go/sep7"); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Error("autor3search-go/sep7 was created despite an unknown benchmark, want no branch")
	}
}

func TestBaselineRejectsTraversalTag(t *testing.T) {
	// Reproduces the reported finding: an unvalidated -tag reaches
	// state.StateDir's filepath.Join, which resolves "..", and os.MkdirAll
	// then creates a directory wherever the traversal lands — long before
	// gitx.CreateBranch's own ref-name rules would ever see the tag. The
	// fix must refuse before any directory outside the cache root is ever
	// created, not just eventually fail on an unrelated later check.
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache dir on this platform")
	}
	marker := filepath.Join(t.TempDir(), "escaped-marker")
	// state.StateDir joins the tag as the 4th path component under the
	// cache dir (cache/autor3search-go/<16-hex-char hash>/<tag>). Compute the
	// traversal relative to a same-depth placeholder rather than cacheDir
	// itself, so evilTag carries exactly enough ".." to reach marker once
	// StateDir's real hash segment is joined in too — an insufficient count
	// would only walk partway out and this test would then pass for the
	// wrong reason.
	placeholderStateDir := filepath.Join(cacheDir, "autor3search-go", strings.Repeat("f", 16))
	evilTag, err := filepath.Rel(placeholderStateDir, marker)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}

	var code int
	stderr := captureStderr(t, func() {
		code = runBaseline([]string{"-C", dir, "-tag", evilTag})
	})
	if code == exitOK {
		t.Fatalf("runBaseline accepted a traversal tag %q, want refusal", evilTag)
	}
	if !strings.Contains(stderr, evilTag) {
		t.Errorf("stderr = %q, want it to name the offending tag %q", stderr, evilTag)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("traversal tag created %s outside the cache root", marker)
	}
}

func TestBaselineAcceptsEmptyBenchmarksList(t *testing.T) {
	// An empty benchmarks: list means "measure everything discovered" and
	// must remain valid — it is not itself an unknown-benchmark configuration.
	dir := copyDemoRepo(t)
	mustInit(t, dir)

	configPath := filepath.Join(dir, config.Path)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Benchmarks = nil
	if err := os.WriteFile(configPath, renderConfig(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "empty benchmarks list")

	if code := runBaseline([]string{"-C", dir, "-tag", "sep8"}); code != exitOK {
		t.Fatalf("runBaseline with an empty benchmarks list = %d, want %d", code, exitOK)
	}
}

// writeInvalidConfig replaces dir's generated config with one that Load
// accepts syntactically but Validate rejects: count below the significance
// floor. That is the realistic case — a repository configured before the
// floor existed — and it is the one where "run init first" is a dead end,
// since init refuses to overwrite an existing config without -force.
func writeInvalidConfig(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, config.Path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected a generated config at %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("count: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineInvalidConfigDoesNotAdviseInit(t *testing.T) {
	dir := copyDemoRepo(t)
	mustInit(t, dir)
	writeInvalidConfig(t, dir)

	var code int
	out := captureStderr(t, func() {
		code = runBaseline([]string{"-C", dir, "-tag", "sep4"})
	})
	if code != exitUsage {
		t.Fatalf("runBaseline = %d, want %d", code, exitUsage)
	}
	if strings.Contains(out, "init` first") {
		t.Errorf("invalid config sends the user to init, which refuses to overwrite it:\n%s", out)
	}
	if !strings.Contains(out, "exists but is invalid") {
		t.Errorf("stderr does not say the config exists but is invalid:\n%s", out)
	}
}

func TestBaselineMissingConfigAdvisesInit(t *testing.T) {
	dir := copyDemoRepo(t) // no mustInit: there is genuinely no config yet

	var code int
	out := captureStderr(t, func() {
		code = runBaseline([]string{"-C", dir, "-tag", "sep4"})
	})
	if code != exitUsage {
		t.Fatalf("runBaseline = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(out, "init` first") {
		t.Errorf("an absent config should point at init:\n%s", out)
	}
}
