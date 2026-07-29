using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class AgentHostSubjectTemplateTests
{
    private sealed class StubIdentity : IIdentityProvider
    {
        public ValueTask<AgentIdentity> GetIdentityAsync(CancellationToken ct = default) =>
            ValueTask.FromResult(new AgentIdentity("some-user", "d-test"));
    }

    private sealed class NoopRenderer : IToastRenderer
    {
        public ValueTask<DateTimeOffset> ShowAsync(ToastRequest toast, CancellationToken ct = default) =>
            ValueTask.FromResult(DateTimeOffset.UtcNow);
    }

    [Theory]
    [InlineData("notify.>")]
    [InlineData("notify.user.desktop")]
    [InlineData("")]
    [InlineData("notify.{{0}}.desktop")]
    public void ValidateSubjectTemplate_throws_when_template_has_no_placeholder(string subjectTemplate)
    {
        // "notify.{{0}}.desktop" in particular: its raw text contains the substring "{0}", so
        // a naive substring check would wrongly accept it, but {{/}} are literal-brace escapes
        // in a composite format string, so string.Format would actually emit the literal text
        // "notify.{0}.desktop" verbatim with no real user-id substitution.
        Assert.Throws<InvalidOperationException>(() => AgentHost.ValidateSubjectTemplate(subjectTemplate));
    }

    [Fact]
    public void ValidateSubjectTemplate_accepts_a_template_with_a_placeholder()
    {
        AgentHost.ValidateSubjectTemplate("notify.user.{0}.desktop"); // must not throw
    }

    [Fact]
    public async Task StartAsync_throws_before_connecting_when_subject_template_has_no_placeholder()
    {
        // A malformed subjectTemplate (e.g. from a settings file/env var with no {0}, or
        // something like "notify.>") must fail startup loudly instead of silently creating
        // an unformatted or accidental wildcard-style subscription. The check must happen
        // before any NATS connection attempt, so this must throw fast even with no NATS
        // server reachable at the configured URL.
        var options = new AgentOptions
        {
            NatsUrl = "nats://127.0.0.1:1", // deliberately unreachable
            SubjectTemplate = "notify.>",
        };

        await Assert.ThrowsAsync<InvalidOperationException>(
            () => AgentHost.StartAsync(options, new StubIdentity(), new NoopRenderer()));
    }
}
