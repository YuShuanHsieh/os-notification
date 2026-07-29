using System.Text.RegularExpressions;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public partial class WindowsUsernameIdentityProviderTests
{
    [GeneratedRegex("^u_[a-z0-9_-]*_[0-9a-f]{8}$")]
    private static partial Regex UserIdShapeRegex();

    [Fact]
    public async Task GetIdentityAsync_resolves_to_u_prefixed_lowercased_username()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => "JDoe");

        var identity = await provider.GetIdentityAsync();

        Assert.StartsWith("u_jdoe_", identity.UserId);
        Assert.Matches(UserIdShapeRegex(), identity.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_strips_a_domain_prefix_if_present()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => @"CONTOSO\JDoe");

        var identity = await provider.GetIdentityAsync();

        Assert.StartsWith("u_jdoe_", identity.UserId);
        Assert.Matches(UserIdShapeRegex(), identity.UserId);
    }

    [Theory]
    [InlineData("John Doe", "john_doe")]
    [InlineData("john.doe", "john_doe")]
    [InlineData("user*name", "user_name")]
    [InlineData("user>name", "user_name")]
    public async Task GetIdentityAsync_sanitizes_unsafe_characters_instead_of_rejecting(
        string rawUsername, string expectedPrefix)
    {
        // The confirmed-exploitable case ("John Doe") in particular: an unsanitized interior
        // space would let the id split a NATS `SUB <subject> [queue-group] <sid>` line into
        // subject + queue-group, silently misrouting the subscription.
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => rawUsername);

        var identity = await provider.GetIdentityAsync();

        Assert.StartsWith($"u_{expectedPrefix}_", identity.UserId);
        Assert.Matches(UserIdShapeRegex(), identity.UserId);
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
    public async Task GetIdentityAsync_still_resolves_when_sanitization_leaves_no_usable_characters(
        string unsafeUsername)
    {
        // The previous review round rejected this case outright ("no usable characters for
        // identity"), but that's no longer necessary: the hash suffix alone guarantees a
        // non-empty, effectively-unique user id regardless of how degenerate the sanitized
        // prefix is, so a username like "***" must now resolve rather than throw.
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => unsafeUsername);

        var identity = await provider.GetIdentityAsync();

        Assert.Matches(UserIdShapeRegex(), identity.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_is_deterministic_for_the_same_input()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => "jdoe");

        var first = await provider.GetIdentityAsync();
        var second = await provider.GetIdentityAsync();

        Assert.Equal(first.UserId, second.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_does_not_collide_two_different_usernames_that_sanitize_identically()
    {
        // The actual bug being fixed: "user.name" and "user_name" both sanitize to the same
        // allowlisted string ("user_name"), so without a hash suffix these two genuinely
        // different Windows accounts would collide onto one identity/NATS subject.
        var providerA = new WindowsUsernameIdentityProvider(getRawUsername: () => "user.name");
        var providerB = new WindowsUsernameIdentityProvider(getRawUsername: () => "user_name");

        var identityA = await providerA.GetIdentityAsync();
        var identityB = await providerB.GetIdentityAsync();

        Assert.StartsWith("u_user_name_", identityA.UserId);
        Assert.StartsWith("u_user_name_", identityB.UserId);
        Assert.NotEqual(identityA.UserId, identityB.UserId);
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
