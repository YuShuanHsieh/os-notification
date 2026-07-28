// Package graphemetext provides Unicode-safe truncation of strings by
// extended grapheme cluster, mirroring GraphemeText.cs (C#) and grapheme.rs
// (Rust) elsewhere in this repository. Truncating by rune (Go's native code
// point) or by byte can split a single user-perceived character — such as an
// emoji built from multiple joined code points — in half; truncating by
// grapheme cluster never does.
package graphemetext

import "github.com/rivo/uniseg"

// Truncate returns s unchanged if it has at most maxClusters extended
// grapheme clusters. Otherwise it returns the first maxClusters-1 clusters
// followed by "…", matching GraphemeText.cs's Truncate exactly (ellipsis
// included in the returned length budget, never splitting a cluster).
// A maxClusters of 0 or less returns the empty string.
func Truncate(s string, maxClusters int) string {
	if maxClusters <= 0 {
		return ""
	}

	gr := uniseg.NewGraphemes(s)
	count := 0
	keepEnd := 0 // byte offset right after the (maxClusters-1)-th cluster
	for gr.Next() {
		count++
		if count > maxClusters {
			return s[:keepEnd] + "…"
		}
		_, to := gr.Positions()
		if count == maxClusters-1 {
			keepEnd = to
		}
	}

	// Reached the end of s without exceeding maxClusters.
	return s
}
