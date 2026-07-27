// Package graphemetext provides Unicode-safe truncation of strings by
// extended grapheme cluster, mirroring GraphemeText.cs (C#) and grapheme.rs
// (Rust) elsewhere in this repository. Truncating by rune (Go's native code
// point) or by byte can split a single user-perceived character — such as an
// emoji built from multiple joined code points — in half; truncating by
// grapheme cluster never does.
package graphemetext

import "github.com/rivo/uniseg"

// Truncate returns s truncated to at most maxClusters extended grapheme
// clusters. If s already has maxClusters or fewer clusters, it is returned
// unchanged. A maxClusters of 0 returns the empty string.
func Truncate(s string, maxClusters int) string {
	if maxClusters <= 0 {
		return ""
	}

	gr := uniseg.NewGraphemes(s)
	count := 0
	end := 0
	for gr.Next() {
		count++
		if count > maxClusters {
			return s[:end]
		}
		_, to := gr.Positions()
		end = to
	}

	// Reached the end of s without exceeding maxClusters.
	return s
}
