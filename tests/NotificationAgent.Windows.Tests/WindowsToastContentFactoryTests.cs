using System.Xml.Linq;
using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using NotificationAgent.Windows;

namespace NotificationAgent.Windows.Tests;

public sealed class WindowsToastContentFactoryTests
{
    [Fact]
    public void Create_IncludesTextAndAttribution()
    {
        var document = CreateDocument(ActionUrl: null);
        var text = document.Descendants("text").ToArray();
        Assert.Equal("Title", text[0].Value);
        Assert.Equal("Message", text[1].Value);
        Assert.Equal("App", text.Single(node =>
            (string?)node.Attribute("placement") == "attribution").Value);
    }

    [Fact]
    public void Create_AddsProtocolButtonForValidHttpsAction()
    {
        var action = Assert.Single(CreateDocument().Descendants("action"));
        Assert.Equal("Open", (string?)action.Attribute("content"));
        Assert.Equal("protocol", (string?)action.Attribute("activationType"));
        Assert.Equal("https://example.com/item", (string?)action.Attribute("arguments"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("http://example.com/item")]
    [InlineData("file:///C:/Windows/System32/cmd.exe")]
    public void Create_OmitsActionForMissingOrUnsafeUrl(string? actionUrl)
    {
        Assert.Empty(CreateDocument(ActionUrl: actionUrl).Descendants("action"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Create_OmitsActionForMissingOrBlankLabel(string? actionLabel)
    {
        Assert.Empty(CreateDocument(ActionLabel: actionLabel).Descendants("action"));
    }

    private static XDocument CreateDocument(
        string? ActionLabel = "Open",
        string? ActionUrl = "https://example.com/item")
    {
        var request = new ToastRequest(
            "Title", "Message", "App", ActionLabel, ActionUrl,
            Array.Empty<InboundNotification>());
        return XDocument.Parse(WindowsToastContentFactory.Create(request).GetContent());
    }
}
