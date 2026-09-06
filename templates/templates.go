// Package templates holds files written into a user's repository by
// `autor3search-go init`.
package templates

import _ "embed"

//go:embed program.md
var programMD []byte

// ProgramMD returns the agent instruction file written into a repository by init.
func ProgramMD() []byte { return programMD }
