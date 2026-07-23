using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class VersionInfoTests
{
    [Theory]
    [InlineData(1, 2, 3, "1.2.3")]
    [InlineData(0, 1, 0, "0.1.0")]
    public void Format_returns_major_minor_build(int major, int minor, int build, string expected)
    {
        var version = new Version(major, minor, build);

        Assert.Equal(expected, VersionInfo.Format(version));
    }

    [Fact]
    public void Format_returns_unknown_when_version_is_null()
    {
        Assert.Equal("unknown", VersionInfo.Format(null));
    }
}
