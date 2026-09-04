//go:build unix

package doctor

import (
	"fmt"
	"syscall"
)

// checkDiskSpace checks available disk space using statfs (unix only).
func checkDiskSpace(dir string) Finding {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return Finding{
			Name:     "disk",
			Detail:   fmt.Sprintf("available disk space: unknown (error: %v)", err),
			Severity: SeverityOK,
		}
	}

	// Available space = available blocks * block size
	availableBytes := uint64(stat.Bavail) * uint64(stat.Bsize)
	availableGB := float64(availableBytes) / (1024.0 * 1024.0 * 1024.0)

	const minGBWarn = 2.0
	severity := SeverityOK
	if availableGB < minGBWarn {
		severity = SeverityWarn
	}

	return Finding{
		Name:     "disk",
		Detail:   fmt.Sprintf("available disk space: %.1f GB", availableGB),
		Severity: severity,
	}
}
