package discover

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write(t, root, "pkg/a_test.go", `package pkg

import "testing"

func TestSomething(t *testing.T) {}

func BenchmarkParse(b *testing.B) {}

func BenchmarkEncode(b *testing.B) {}

// helper is not a benchmark despite the name prefix on the next line.
func BenchmarkHelperNotABenchmark(x int) {}
`)
	write(t, root, "pkg/a.go", `package pkg

func BenchmarkNotInTestFile(b *testing.B) {}
`)
	write(t, root, "vendor/dep/v_test.go", `package dep

import "testing"

func BenchmarkVendored(b *testing.B) {}
`)
	write(t, root, "testdata/fixture_test.go", `package fixture

import "testing"

func BenchmarkFixture(b *testing.B) {}
`)
	write(t, root, ".autor3search/frozen/pkg/a_test.go", `package pkg

import "testing"

func BenchmarkFrozenCopy(b *testing.B) {}
`)
	return root
}

func TestBenchmarksFindsOnlyRealOnes(t *testing.T) {
	root := fixture(t)
	got, err := Benchmarks(root)
	if err != nil {
		t.Fatalf("Benchmarks: %v", err)
	}
	names := BaseNames(got)
	want := []string{"BenchmarkEncode", "BenchmarkParse"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Benchmarks = %v, want %v", names, want)
	}
}

func TestBenchmarksRecordsLocation(t *testing.T) {
	root := fixture(t)
	got, err := Benchmarks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range got {
		if b.File != "pkg/a_test.go" {
			t.Errorf("%s File = %q, want pkg/a_test.go", b.Name, b.File)
		}
		if b.Dir != "pkg" {
			t.Errorf("%s Dir = %q, want pkg", b.Name, b.Dir)
		}
	}
}

func TestTestFilesSkipsIgnoredDirs(t *testing.T) {
	root := fixture(t)
	got, err := TestFiles(root, nil)
	if err != nil {
		t.Fatalf("TestFiles: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"pkg/a_test.go"}) {
		t.Fatalf("TestFiles = %v, want [pkg/a_test.go]", got)
	}
}

func TestTestFilesHonoursExclude(t *testing.T) {
	root := fixture(t)
	got, err := TestFiles(root, []string{"pkg/a_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("TestFiles = %v, want empty after exclude", got)
	}
}

func TestBenchmarksOnEmptyRepo(t *testing.T) {
	got, err := Benchmarks(t.TempDir())
	if err != nil {
		t.Fatalf("Benchmarks: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Benchmarks = %v, want empty", got)
	}
}
