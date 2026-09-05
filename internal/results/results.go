// Package results reads and appends the experiment log.
package results

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Path is the log location relative to the repository root.
const Path = "results.tsv"

// Header is the first line of every log file.
const Header = "commit\tscore\tbest_bench_delta\tallocs_delta\tstatus\tdescription"

// maxDescriptionLen is the longest Description Append will write verbatim.
// -desc has no length cap of its own, and an agent pasting something large
// (a stack trace, a diff) would otherwise produce a results.tsv line long
// enough to exceed maxScanTokenSize, after which every future Load (and so
// every future `report` and `baseline -force`) fails until a human edits
// the file by hand. 256 characters is generous for a one-line experiment
// summary and keeps the row both readable and safely far from that limit.
const maxDescriptionLen = 256

// maxScanTokenSize is the buffer Load gives bufio.Scanner, well above its
// default 64 KiB token limit. Truncating Description in Append keeps a
// well-behaved row far under this, but the buffer is still sized generously
// so a single malformed or hand-edited line does not trip ErrTooLong on
// otherwise-reasonable content.
const maxScanTokenSize = 1 << 20 // 1 MiB

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

// truncateDescription caps s at maxDescriptionLen runes, appending "..." when
// it had to cut. Truncating by rune count (not byte count) means a
// multi-byte UTF-8 character is never split in half. This is the write-side
// half of the over-long-description fix: an agent pasting something large
// (a stack trace, a diff) into -desc must not be able to produce a
// results.tsv line long enough to jam every future Load.
func truncateDescription(s string) string {
	r := []rune(s)
	if len(r) <= maxDescriptionLen {
		return s
	}
	return string(r[:maxDescriptionLen]) + "..."
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
		clean(r.Commit), r.Score, r.BestBenchDelta, r.AllocsDelta, clean(r.Status),
		truncateDescription(clean(r.Description))); err != nil {
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
	// bufio.Scanner's default 64 KiB token limit is easy to exceed with a
	// single over-long -desc (a pasted stack trace, a diff): Append caps
	// Description going forward, but a line written before that fix (or
	// hand-edited in) would otherwise trip bufio.ErrTooLong with no
	// indication of which line or why. Buffer(..., maxScanTokenSize) raises
	// the limit well above what any Append-written row can ever reach, and
	// the explicit check below turns the rare line that still exceeds it
	// into a message naming the file and line instead of a bare
	// "bufio.Scanner: token too long".
	sc.Buffer(make([]byte, 0, 64*1024), maxScanTokenSize)
	lineNo := 1
	for ; sc.Scan(); lineNo++ {
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
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("%s:%d: line too long (over %d bytes) — trim or remove the offending row",
				path, lineNo, maxScanTokenSize)
		}
		return nil, err
	}
	return rows, nil
}
