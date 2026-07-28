using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class WindowsUsernameIdentityProviderTests
{
    [Fact]
    public async Task GetIdentityAsync_resolves_to_u_prefixed_lowercased_username()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => "JDoe");

        var identity = await provider.GetIdentityAsync();

        Assert.Equal("u_jdoe", identity.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_strips_a_domain_prefix_if_present()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => @"CONTOSO\JDoe");

        var identity = await provider.GetIdentityAsync();

        Assert.Equal("u_jdoe", identity.UserId);
    }

    [Theory]
    [InlineData("j.doe")]
    [InlineData("j*doe")]
    [InlineData("j>doe")]
    public async Task GetIdentityAsync_throws_when_username_contains_a_nats_subject_wildcard_char(string unsafeUsername)
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => unsafeUsername);

        await Assert.ThrowsAsync<InvalidOperationException>(() => provider.GetIdentityAsync().AsTask());
    }

    [Fact]
    public async Task GetIdentityAsync_throws_when_username_resolves_to_empty()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => "   ");

        await Assert.ThrowsAsync<InvalidOperationException>(() => provider.GetIdentityAsync().AsTask());
    }

    [Fact]
    public async Task GetIdentityAsync_uses_device_id_override_when_provided()
    {
        var provider = new WindowsUsernameIdentityProvider(
            deviceIdOverride: "d-fixed-for-test", getRawUsername: () => "jdoe");

        var identity = await provider.GetIdentityAsync();

        Assert.Equal("d-fixed-for-test", identity.DeviceId);
    }
}
