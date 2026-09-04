// Package discover finds benchmarks and test files in a Go repository
// without invoking the go tool, so it works on a tree that does not build.
package discover

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Benchmark is one discovered benchmark function.
type Benchmark struct {
	// Name is the function name, e.g. "BenchmarkParse".
	Name string
	// File is the repo-relative file declaring it.
	File string
	// Dir is the repo-relative directory of that file, "." at the root.
	Dir string
}

// skipDir reports directories the go tool itself ignores.
func skipDir(name string) bool {
	if name == "vendor" || name == "testdata" {
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// walkTestFiles calls fn for each repo-relative _test.go path.
func walkTestFiles(root string, fn func(rel string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		return fn(filepath.ToSlash(rel))
	})
}

// isBenchmarkFunc reports whether fd is func BenchmarkX(b *testing.B).
func isBenchmarkFunc(fd *ast.FuncDecl) bool {
	if fd.Recv != nil || fd.Name == nil || !strings.HasPrefix(fd.Name.Name, "Benchmark") {
		return false
	}
	if fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return false
	}
	star, ok := fd.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "B"
}

// Benchmarks returns every benchmark declared in the repository, sorted by name.
func Benchmarks(root string) ([]Benchmark, error) {
	var out []Benchmark
	fset := token.NewFileSet()
	err := walkTestFiles(root, func(rel string) error {
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			// An unparseable test file is not fatal to discovery; skip it.
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || !isBenchmarkFunc(fd) {
				continue
			}
			out = append(out, Benchmark{Name: fd.Name.Name, File: rel, Dir: dir})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover benchmarks: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// TestFiles returns every repo-relative _test.go path, minus exclude, sorted.
func TestFiles(root string, exclude []string) ([]string, error) {
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[filepath.ToSlash(e)] = true
	}
	var out []string
	err := walkTestFiles(root, func(rel string) error {
		if !skip[rel] {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover test files: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// BaseNames returns the benchmark function names, sorted and deduplicated.
func BaseNames(bs []Benchmark) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range bs {
		if !seen[b.Name] {
			seen[b.Name] = true
			out = append(out, b.Name)
		}
	}
	sort.Strings(out)
	return out
}
