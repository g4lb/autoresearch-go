# demo

This module exists solely as a fixture for autor3search-go's own integration tests. The `CountWords` function is intentionally implemented with quadratic string concatenation (rebuilding the string on every rune by using `word = word + string(r)` inside a loop), which creates real, measurable performance headroom. This slowness must not be "fixed" because Tasks 15 and 17's integration tests depend on there being sufficient gap between the baseline implementation and an optimized version to verify that the tool correctly identifies and keeps genuine performance improvements.
