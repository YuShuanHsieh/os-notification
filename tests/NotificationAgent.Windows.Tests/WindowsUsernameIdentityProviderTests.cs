using System.Text.RegularExpressions;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public partial class WindowsUsernameIdentityProviderTests
{
    [GeneratedRegex("^[a-z0-9_-]+$")]
    private static partial Regex UserIdShapeRegex();

    [Fact]
    public async Task GetIdentityAsync_resolves_to_lowercased_username()
    {
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => "JDoe");

        var identity = await provider.GetIdentityAsync();

        Assert.Equal("jdoe", identity.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_drops_a_domain_prefix_if_present()
    {
        // Deployments using this provider are expected to guarantee account-name uniqueness
        // themselves, so the domain/machine qualifier is discarded rather than folded into
        // the identity.
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => @"CORP\JDoe");

        var identity = await provider.GetIdentityAsync();

        Assert.Equal("jdoe", identity.UserId);
    }

    [Fact]
    public async Task GetIdentityAsync_domain_qualified_and_bare_usernames_collapse_to_the_same_id()
    {
        // Documents the deliberate behavior: with the domain qualifier dropped,
        // identically-named accounts in different domains (and their bare equivalent) all
        // resolve to the same identity.
        var providerA = new WindowsUsernameIdentityProvider(getRawUsername: () => @"CORP\jdoe");
        var providerB = new WindowsUsernameIdentityProvider(getRawUsername: () => @"CONTOSO\jdoe");
        var providerC = new WindowsUsernameIdentityProvider(getRawUsername: () => "jdoe");

        var identityA = await providerA.GetIdentityAsync();
        var identityB = await providerB.GetIdentityAsync();
        var identityC = await providerC.GetIdentityAsync();

        Assert.Equal("jdoe", identityA.UserId);
        Assert.Equal("jdoe", identityB.UserId);
        Assert.Equal("jdoe", identityC.UserId);
    }

    [Theory]
    [InlineData("John Doe", "john_doe")]
    [InlineData("john.doe", "john_doe")]
    [InlineData("user*name", "user_name")]
    [InlineData("user>name", "user_name")]
    public async Task GetIdentityAsync_sanitizes_unsafe_characters_instead_of_rejecting(
        string rawUsername, string expected)
    {
        // The confirmed-exploitable case ("John Doe") in particular: an unsanitized interior
        // space would let the id split a NATS `SUB <subject> [queue-group] <sid>` line into
        // subject + queue-group, silently misrouting the subscription.
        var provider = new WindowsUsernameIdentityProvider(getRawUsername: () => rawUsername);

        var identity = await provider.GetIdentityAsync();

        Assert.Equal(expected, identity.UserId);
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
        // sanitize replaces rather than strips characters, so an all-punctuation username
        // still resolves rather than throwing.
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
    public async Task GetIdentityAsync_uses_device_id_override_when_provided()
    {
        var provider = new WindowsUsernameIdentityProvider(
            deviceIdOverride: "d-fixed-for-test", getRawUsername: () => "jdoe");

        var identity = await provider.GetIdentityAsync();

        Assert.Equal("d-fixed-for-test", identity.DeviceId);
    }
}
