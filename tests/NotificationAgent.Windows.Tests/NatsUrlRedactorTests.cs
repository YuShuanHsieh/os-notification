using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class NatsUrlRedactorTests
{
    [Fact]
    public void Redact_replaces_userinfo_with_stars_and_drops_the_password()
    {
        var redacted = NatsUrlRedactor.Redact("nats://user:password@host:4222");

        Assert.Equal("nats://***@host:4222/", redacted);
        Assert.DoesNotContain("password", redacted);
        Assert.DoesNotContain("user:", redacted);
    }

    [Fact]
    public void Redact_leaves_a_url_without_userinfo_unchanged()
    {
        var url = "nats://127.0.0.1:4222";

        var redacted = NatsUrlRedactor.Redact(url);

        Assert.Equal(new Uri(url).ToString(), redacted);
    }

    [Fact]
    public void Redact_returns_a_safe_marker_when_it_does_not_parse_as_an_absolute_uri()
    {
        var notAUrl = "not a url";

        var redacted = NatsUrlRedactor.Redact(notAUrl);

        Assert.Equal(NatsUrlRedactor.InvalidUrlMarker, redacted);
    }

    [Fact]
    public void Redact_replaces_malformed_input_containing_embedded_userinfo_with_the_marker()
    {
        // Even though this unparseable string still contains a credential-like substring, it
        // must never leak through verbatim: failing to parse must fail closed.
        var malformed = "not a url but has user:supersecret@ embedded in it";

        var redacted = NatsUrlRedactor.Redact(malformed);

        Assert.Equal(NatsUrlRedactor.InvalidUrlMarker, redacted);
        Assert.DoesNotContain("supersecret", redacted);
    }

    [Fact]
    public void Redact_replaces_userinfo_when_only_a_username_is_present()
    {
        var redacted = NatsUrlRedactor.Redact("nats://user@host:4222");

        Assert.Equal("nats://***@host:4222/", redacted);
    }
}
