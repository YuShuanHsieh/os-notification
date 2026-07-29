using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class DeviceIdStoreTests
{
    [Fact]
    public void GetOrCreate_returns_the_override_when_it_is_non_blank()
    {
        var result = DeviceIdStore.GetOrCreate("d-fixed-for-test");

        Assert.Equal("d-fixed-for-test", result);
    }

    [Theory]
    [InlineData("   ")]
    [InlineData("\t")]
    public void GetOrCreate_ignores_a_whitespace_only_override(string overrideValue)
    {
        // A settings-file deviceId of "   " must not "win" with garbage; it should fall
        // through to the persisted/generated device id the same as a null/absent override.
        var withWhitespaceOverride = DeviceIdStore.GetOrCreate(overrideValue);
        var withNoOverride = DeviceIdStore.GetOrCreate(null);

        Assert.NotEqual(overrideValue, withWhitespaceOverride);
        Assert.False(string.IsNullOrWhiteSpace(withWhitespaceOverride));
        Assert.Equal(withNoOverride, withWhitespaceOverride);
    }
}
