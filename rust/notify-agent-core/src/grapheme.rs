use unicode_segmentation::UnicodeSegmentation;

/// Truncate to at most `max_graphemes` extended grapheme clusters (the design
/// doc's "product limit" unit), ellipsis included in the limit.
pub fn truncate(value: &str, max_graphemes: usize) -> String {
    assert!(max_graphemes >= 1, "max_graphemes must be >= 1");
    if value.graphemes(true).count() <= max_graphemes {
        return value.to_string();
    }
    let kept: String = value.graphemes(true).take(max_graphemes - 1).collect();
    format!("{kept}…")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn returns_short_strings_unchanged() {
        assert_eq!(truncate("hello", 5), "hello");
        assert_eq!(truncate("", 5), "");
    }

    #[test]
    fn truncates_to_limit_with_ellipsis() {
        // 6 chars, limit 5 → 4 kept + "…" = 5 grapheme clusters total
        assert_eq!(truncate("abcdef", 5), "abcd…");
    }

    #[test]
    fn counts_grapheme_clusters_not_chars() {
        // Family emoji: 1 grapheme cluster, 7 chars / 11 UTF-16 units
        let family = "\u{1F469}\u{200D}\u{1F469}\u{200D}\u{1F467}\u{200D}\u{1F466}";
        assert_eq!(truncate(family, 1), family);
        let two = format!("{family}{family}");
        assert_eq!(truncate(&two, 2), two);
        let three = format!("{family}{family}{family}");
        assert_eq!(truncate(&three, 2), format!("{family}…"));
    }
}
