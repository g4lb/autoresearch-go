// Package scope decides which files an agent is allowed to modify.
package scope

import (
	"path"
	"strings"
)

type rule struct {
	prefix    string // normalized directory prefix, "" means repository root
	recursive bool
}

// Matcher tests repo-relative paths against a set of go-style patterns.
type Matcher struct {
	rules []rule
}

// New compiles patterns such as "./...", "./internal/..." or "./pkg".
func New(patterns []string) *Matcher {
	m := &Matcher{}
	for _, p := range patterns {
		p = path.Clean(strings.TrimPrefix(strings.TrimSpace(p), "./"))
		recursive := false
		if p == "..." {
			p, recursive = "", true
		} else if strings.HasSuffix(p, "/...") {
			p, recursive = strings.TrimSuffix(p, "/..."), true
		}
		if p == "." {
			p = ""
		}
		m.rules = append(m.rules, rule{prefix: p, recursive: recursive})
	}
	return m
}

// Match reports whether rel is inside the allowed scope.
func (m *Matcher) Match(rel string) bool {
	rel = path.Clean(strings.TrimPrefix(rel, "./"))
	for _, r := range m.rules {
		if r.recursive {
			if r.prefix == "" || rel == r.prefix || strings.HasPrefix(rel, r.prefix+"/") {
				return true
			}
			continue
		}
		if path.Dir(rel) == r.prefix || (r.prefix == "" && path.Dir(rel) == ".") {
			return true
		}
	}
	return false
}
