// Package results reads and appends the experiment log.
package results

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Path is the log location relative to the repository root.
const Path = "results.tsv"

// Header is the first line of every log file.
const Header = "commit\tscore\tbest_bench_delta\tallocs_delta\tstatus\tdescription"

// Row is one logged experiment.
// Field order must be kept in step with Header, Append's format string, and Load's parts[0..5] indexing.
type Row struct {
	Commit string
	// Score is the geomean of per-benchmark ratios; 1.0 means no change.
	Score float64
	// BestBenchDelta is the largest single-benchmark improvement, percent.
	BestBenchDelta float64
	// AllocsDelta is the change in allocs/op for the primary benchmark.
	AllocsDelta float64
	// Status is keep, discard, fail or crash.
	Status string
	// Description is a short human summary of what was tried.
	Description string
}

// clean makes a field safe for a tab-separated single-line record.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// Append adds one row, creating the file with a header when needed.
func Append(path string, r Row) (err error) {
	_, statErr := os.Stat(path)
	isNew := os.IsNotExist(statErr)

	f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return fmt.Errorf("open %s: %w", path, ferr)
	}
	defer func() {
		// Close surfaces deferred I/O errors (full disk, quota, network FS).
		// Only report it if nothing has already failed.
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()

	if isNew {
		if _, err := fmt.Fprintln(f, Header); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(f, "%s\t%.4f\t%.2f\t%.2f\t%s\t%s\n",
		clean(r.Commit), r.Score, r.BestBenchDelta, r.AllocsDelta, clean(r.Status), clean(r.Description)); err != nil {
		return err
	}
	// Sync ensures the log is durable across system crashes, since it is the sole record of overnight runs.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}

// Load reads every row. A missing file is an empty log, not an error.
// Strict by design: a malformed line fails the whole load rather than being skipped.
// Sanitising makes malformed rows nearly impossible, so one is a real signal — a torn write or hand edit —
// and the error names the exact file and line so it can be fixed. Silently dropping rows would let a
// corrupted log masquerade as a short one, which is worse for a file that is the sole record of an
// overnight run.
func Load(path string) ([]Row, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var rows []Row
	sc := bufio.NewScanner(f)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := sc.Text()
		if line == "" || line == Header {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 6 {
			return nil, fmt.Errorf("%s:%d: got %d fields, want 6", path, lineNo, len(parts))
		}
		score, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: score: %w", path, lineNo, err)
		}
		best, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: best_bench_delta: %w", path, lineNo, err)
		}
		allocs, err := strconv.ParseFloat(parts[3], 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: allocs_delta: %w", path, lineNo, err)
		}
		rows = append(rows, Row{
			Commit: parts[0], Score: score, BestBenchDelta: best,
			AllocsDelta: allocs, Status: parts[4], Description: parts[5],
		})
	}
	return rows, sc.Err()
}
