package graphemetext

import "testing"

func TestTruncate_ShortAsciiStringUnchanged(t *testing.T) {
	got := Truncate("hello", 10)
	if got != "hello" {
		t.Errorf("Truncate(%q, 10) = %q, want %q", "hello", got, "hello")
	}
}

func TestTruncate_LongAsciiStringTruncatedToExactLimit(t *testing.T) {
	got := Truncate("abcdefghij", 5)
	want := "abcde"
	if got != want {
		t.Errorf("Truncate(%q, 5) = %q, want %q", "abcdefghij", got, want)
	}
	if runeCount := len([]rune(got)); runeCount != 5 {
		t.Errorf("expected exactly 5 characters, got %d (%q)", runeCount, got)
	}
}

func TestTruncate_NeverSplitsMultiCodepointEmoji(t *testing.T) {
	// Family emoji: woman + ZWJ + woman + ZWJ + girl + ZWJ + boy.
	// One grapheme cluster, four runes joined by zero-width joiners.
	family := "\U0001F469‍\U0001F469‍\U0001F467‍\U0001F466"

	// Limit of 1 cluster falls squarely in the middle of the family emoji's
	// runes; the whole cluster must be kept, never split.
	got := Truncate(family, 1)
	if got != family {
		t.Errorf("Truncate(family, 1) = %q, want the full family emoji unchanged (%q)", got, family)
	}

	// Two clusters: family + a plain "a" grapheme. Limit of 1 must keep only
	// the family cluster intact, not a partial one.
	two := family + "a"
	got = Truncate(two, 1)
	if got != family {
		t.Errorf("Truncate(two, 1) = %q, want %q (family cluster kept whole)", got, family)
	}

	// Two full family emojis, limit of 1: must keep exactly the first family
	// cluster and never a partial second one.
	twoFamilies := family + family
	got = Truncate(twoFamilies, 1)
	if got != family {
		t.Errorf("Truncate(twoFamilies, 1) = %q, want %q", got, family)
	}

	// At the exact cluster count, string is unchanged.
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
