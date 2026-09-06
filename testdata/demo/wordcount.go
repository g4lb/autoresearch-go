// Package demo is a fixture for autor3search-go's own integration tests.
// The implementation is intentionally suboptimal.
package demo

import "strings"

// CountWords returns how many times each lowercase word appears in s.
// Words are separated by whitespace; surrounding punctuation is stripped.
func CountWords(s string) map[string]int {
	counts := map[string]int{}
	for _, field := range strings.Fields(s) {
		word := ""
		for _, r := range field {
			if r >= 'A' && r <= 'Z' {
				r = r + ('a' - 'A')
			}
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				// Deliberately quadratic: rebuilds the string every rune.
				word = word + string(r)
			}
		}
		if word != "" {
			counts[word]++
		}
	}
	return counts
}
