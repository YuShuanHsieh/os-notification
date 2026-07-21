using NotificationAgent.Core.Rendering;

namespace NotificationAgent.Core.Tests;

public sealed class ActionUrlPolicyTests
{
    [Theory]
    [InlineData("https://example.com")]
    [InlineData("https://example.com/path")]
    [InlineData("https://example.com/path?one=1&two=2")]
    [InlineData("https://localhost:8443/path")]
    [InlineData("https://127.0.0.1/path")]
    [InlineData("https://[::1]/path")]
    public void TryCreate_AcceptsValidHttpsUrl(string value)
    {
        var result = ActionUrlPolicy.TryCreate(value, out var uri);

        Assert.True(result);
        Assert.Equal(Uri.UriSchemeHttps, uri.Scheme);
    }

    [Theory]
    [InlineData("")]
    [InlineData("not-a-url")]
    [InlineData("http://example.com")]
    [InlineData("file:///C:/Windows/System32/cmd.exe")]
    [InlineData("javascript:alert(1)")]
    [InlineData("https://")]
    [InlineData("https://user:password@example.com")]
    [InlineData(@"https:\\example.com\path")]
    public void TryCreate_RejectsUnsafeOrMalformedUrl(string value)
    {
        Assert.False(ActionUrlPolicy.TryCreate(value, out _));
    }

    [Fact]
    public void TryCreate_RejectsOversizedUrl()
    {
        var value = "https://example.com/" +
                    new string('a', ActionUrlPolicy.MaxUrlLength);

        Assert.False(ActionUrlPolicy.TryCreate(value, out _));
    }
}
