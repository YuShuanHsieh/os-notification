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
    [InlineData("John Doe", "u_john_doe")]
    [InlineData("john.doe", "u_john_doe")]
    [InlineData("user*name", "u_user_name")]
    [InlineData("user>name", "u_user_name")]
    public async Task GetIdentityAsync_sanitizes_unsafe_characters_instead_of_rejecting(
        string rawUsername, string expectedUserId)
    {
        // The confirmed-exploitable case ("John Doe") in particular: an unsanitized interior
        // space would let the id split a NATS `SUB <subject> [queue-group] <sid>` line into
        // subject + queue-group, silently misrouting the subscription.
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => rawUsername);

        var identity = await provider.GetIdentityAsync();

        Assert.Equal(expectedUserId, identity.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_throws_when_username_resolves_to_empty()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => "   ");

        await Assert.ThrowsAsync<InvalidOperationException>(() => provider.GetIdentityAsync().AsTask());
    }

    [Theory]
    [InlineData("***")]
    [InlineData("...")]
    public async Task GetIdentityAsync_throws_when_sanitization_leaves_no_usable_characters(string unsafeUsername)
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => unsafeUsername);

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
