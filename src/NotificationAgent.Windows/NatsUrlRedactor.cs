// src/NotificationAgent.Windows/NatsUrlRedactor.cs
namespace NotificationAgent.Windows;

/// <summary>Redacts userinfo (credentials) embedded in a NATS URL before it is ever logged
/// (feature: logging). A NATS URL can carry a username/password directly, e.g.
/// <c>nats://user:password@host:4222</c>, and the settings file (feature: app settings file)
/// makes it more likely an operator embeds a credential there rather than using a separate
/// creds-file/auth-service mechanism. Startup logs the resolved NATS URL for diagnostics, so
/// that value must never reach the log verbatim.</summary>
internal static class NatsUrlRedactor
{
    /// <summary>A safe, fixed marker returned in place of input that failed to parse as an
    /// absolute URI. Failing closed here (rather than passing the unparsed string through
    /// unchanged) matters because a malformed value can still contain a credential-like
    /// substring (e.g. embedded userinfo) that <see cref="Uri"/> couldn't make sense of as a
    /// whole; letting it through verbatim would defeat the point of redaction.</summary>
    internal const string InvalidUrlMarker = "<invalid URL>";

    /// <summary>Returns <paramref name="url"/> with any userinfo replaced by <c>***</c>, or
    /// unchanged if it carries no userinfo. Returns <see cref="InvalidUrlMarker"/> instead of
    /// the raw input if it doesn't parse as an absolute URI.</summary>
    public static string Redact(string url)
    {
        if (!Uri.TryCreate(url, UriKind.Absolute, out var uri))
        {
            return InvalidUrlMarker;
        }

        if (!string.IsNullOrEmpty(uri.UserInfo))
        {
            var builder = new UriBuilder(uri) { UserName = "***", Password = string.Empty };
            return builder.Uri.ToString();
        }

        return url;
    }
}
