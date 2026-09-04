//go:build tools

// Package tools anchors build-time dependencies that no production code
// imports yet, so `go mod tidy` cannot drop their pinned versions.
//
// golang.org/x/perf is pinned to a version whose go directive is 1.18.
// The latest x/perf requires go 1.26, which would break this module's
// go 1.21 floor. internal/bench imports it in a later task; until then
// this blank import keeps the pin.
//
// DELETE THIS FILE once internal/bench imports golang.org/x/perf directly.
package tools

import _ "golang.org/x/perf/benchfmt"
