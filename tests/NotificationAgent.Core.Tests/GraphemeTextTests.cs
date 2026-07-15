using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class GraphemeTextTests
{
    [Fact]
    public void Returns_short_strings_unchanged()
    {
        Assert.Equal("hello", GraphemeText.Truncate("hello", 5));
        Assert.Equal("", GraphemeText.Truncate("", 5));
    }

    [Fact]
    public void Truncates_to_limit_with_ellipsis()
    {
        // 6 chars, limit 5 → 4 kept + "…" = 5 grapheme clusters total
        Assert.Equal("abcd…", GraphemeText.Truncate("abcdef", 5));
    }

    [Fact]
    public void Counts_grapheme_clusters_not_chars()
    {
        // Family emoji (woman+woman+girl+boy joined by U+200D zero-width joiners):
        // 1 grapheme cluster, 11 UTF-16 code units
        var family = "\U0001F469‍\U0001F469‍\U0001F467‍\U0001F466";
        Assert.Equal(family, GraphemeText.Truncate(family, 1));         // fits: 1 cluster
        Assert.Equal(family + family, GraphemeText.Truncate(family + family, 2));
        Assert.Equal(family + "…", GraphemeText.Truncate(family + family + family, 2));
    }
}
