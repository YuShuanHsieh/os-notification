using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class TrayApplicationContextTests
{
    [Theory]
    [InlineData("")]
    [InlineData(@"Z:\this\path\does\not\exist.exe")]
    [InlineData("not a path at all \0 with a null char")]
    public void TryExtractApplicationIcon_returns_null_instead_of_throwing_for_a_bad_path(string badPath)
    {
        // Icon.ExtractAssociatedIcon can throw for a bad path (not just return null); a
        // failed icon load must never crash startup.
        var icon = TrayApplicationContext.TryExtractApplicationIcon(badPath);

        Assert.Null(icon);
    }
}
