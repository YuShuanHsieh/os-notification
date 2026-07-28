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
    public void Redact_returns_input_unchanged_when_it_does_not_parse_as_an_absolute_uri()
    {
        var notAUrl = "not a url";

        var redacted = NatsUrlRedactor.Redact(notAUrl);

        Assert.Equal(notAUrl, redacted);
    }

    [Fact]
    public void Redact_replaces_userinfo_when_only_a_username_is_present()
    {
        var redacted = NatsUrlRedactor.Redact("nats://user@host:4222");

        Assert.Equal("nats://***@host:4222/", redacted);
    }
}
