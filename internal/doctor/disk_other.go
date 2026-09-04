//go:build !unix

package doctor

// checkDiskSpace returns a "not checked on this platform" state for non-unix systems.
func checkDiskSpace(dir string) Finding {
	return Finding{
		Name:     "disk",
		Detail:   "available disk space: not checked on this platform",
		Severity: SeverityNotApplicable,
	}
}
