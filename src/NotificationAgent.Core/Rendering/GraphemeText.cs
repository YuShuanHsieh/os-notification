using System.Globalization;

namespace NotificationAgent.Core.Rendering;

public static class GraphemeText
{
    /// <summary>Truncate to at most <paramref name="maxGraphemes"/> extended grapheme
    /// clusters (the design doc's "product limit" unit), ellipsis included.</summary>
    public static string Truncate(string value, int maxGraphemes)
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(maxGraphemes, 1);
        var info = new StringInfo(value);
        if (info.LengthInTextElements <= maxGraphemes)
        {
            return value;
        }

        return info.SubstringByTextElements(0, maxGraphemes - 1) + "…";
    }
}
