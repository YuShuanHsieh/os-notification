package graphemetext

import "testing"

func TestTruncate_ShortAsciiStringUnchanged(t *testing.T) {
	got := Truncate("hello", 10)
	if got != "hello" {
		t.Errorf("Truncate(%q, 10) = %q, want %q", "hello", got, "hello")
	}
}

func TestTruncate_LongAsciiStringKeepsLimitMinusOnePlusEllipsis(t *testing.T) {
	// Matches GraphemeText.cs exactly: SubstringByTextElements(0, maxGraphemes-1) + "…".
	got := Truncate("abcdefghij", 5)
	want := "abcd…"
	if got != want {
		t.Errorf("Truncate(%q, 5) = %q, want %q", "abcdefghij", got, want)
	}
}

func TestTruncate_NeverSplitsMultiCodepointEmoji(t *testing.T) {
	// Family emoji: woman + ZWJ + woman + ZWJ + girl + ZWJ + boy.
	// One grapheme cluster, four runes joined by zero-width joiners.
	family := "\U0001F469‍\U0001F469‍\U0001F467‍\U0001F466"

	// Exactly at the limit: no truncation, no ellipsis.
	got := Truncate(family, 1)
	if got != family {
		t.Errorf("Truncate(family, 1) = %q, want the full family emoji unchanged (%q)", got, family)
	}

	// Two clusters (family + "a"), limit 1: the kept portion is 0 clusters
	// (maxClusters-1 = 0) plus the ellipsis — never a partial family cluster.
	two := family + "a"
	got = Truncate(two, 1)
	if got != "…" {
		t.Errorf("Truncate(two, 1) = %q, want %q", got, "…")
	}

	// Three clusters (family + "a" + "b"), limit 2: kept portion is exactly
	// 1 cluster (the whole family, never split) plus the ellipsis.
	three := family + "a" + "b"
	got = Truncate(three, 2)
	want := family + "…"
	if got != want {
		t.Errorf("Truncate(three, 2) = %q, want %q (family cluster kept whole)", got, want)
	}

	// At the exact cluster count, string is unchanged.
	twoFamilies := family + family
	got = Truncate(twoFamilies, 2)
	if got != twoFamilies {
		t.Errorf("Truncate(twoFamilies, 2) = %q, want unchanged %q", got, twoFamilies)
	}
}

func TestTruncate_ZeroMaxClustersReturnsEmptyString(t *testing.T) {
	got := Truncate("hello", 0)
	if got != "" {
		t.Errorf("Truncate(%q, 0) = %q, want empty string", "hello", got)
	}

	got = Truncate("\U0001F469‍\U0001F469‍\U0001F467‍\U0001F466", 0)
	if got != "" {
		t.Errorf("Truncate(family, 0) = %q, want empty string", got)
	}
}

func TestTruncate_EmptyStringUnchanged(t *testing.T) {
	got := Truncate("", 5)
	if got != "" {
		t.Errorf("Truncate(\"\", 5) = %q, want empty string", got)
	}
}
