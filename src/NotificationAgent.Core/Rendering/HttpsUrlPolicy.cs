namespace NotificationAgent.Core.Rendering;

public static class HttpsUrlPolicy
{
    public const int MaxUrlLength = 2048;

    public static bool TryCreate(string? value, out Uri uri)
    {
        uri = null!;

        if (string.IsNullOrWhiteSpace(value)
            || value.Length > MaxUrlLength
            || !Uri.TryCreate(value, UriKind.Absolute, out var candidate)
            || !candidate.IsWellFormedOriginalString()
            || candidate.Scheme != Uri.UriSchemeHttps
            || string.IsNullOrWhiteSpace(candidate.Host)
            || !string.IsNullOrEmpty(candidate.UserInfo)
            || Uri.CheckHostName(candidate.IdnHost) == UriHostNameType.Unknown)
        {
            return false;
        }

        uri = candidate;
        return true;
    }
}
