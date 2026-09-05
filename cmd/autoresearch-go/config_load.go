package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/g4lb/autoresearch-go/internal/config"
)

// loadConfig reads root's config and reports a usable error when it cannot.
//
// It distinguishes "no config yet" from "a config exists but is invalid":
// init refuses to overwrite an existing config.yaml without -force, so
// telling an agent to run init when one is already there just hands it a
// second error and no path forward. Only a genuinely absent config should
// point at init; an existing-but-invalid one (including a count below the
// significance floor that config.Validate enforces) needs the named field
// corrected in place.
//
// It also returns the config's path, which callers need for the integrity
// hash recorded at baseline, and an exit code: exitOK when cfg is usable,
// exitUsage after the reason has been written to stderr.
func loadConfig(cmdName, root string) (config.Config, string, int) {
	configPath := filepath.Join(root, config.Path)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "autoresearch-go %s: no config at %s\n", cmdName, configPath)
		fmt.Fprintln(os.Stderr, "run `autoresearch-go init` first.")
		return config.Config{}, configPath, exitUsage
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autoresearch-go %s: %v\n", cmdName, err)
		fmt.Fprintf(os.Stderr, "%s exists but is invalid; correct the named field and try again "+
			"(init will refuse to regenerate it without -force).\n", configPath)
		return config.Config{}, configPath, exitUsage
	}
	return cfg, configPath, exitOK
}
