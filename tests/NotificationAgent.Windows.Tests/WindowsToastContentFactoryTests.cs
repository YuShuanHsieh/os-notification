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
        var document = CreateDocument(actionUrl: null);
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
        Assert.Empty(CreateDocument(actionUrl: actionUrl).Descendants("action"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Create_OmitsActionForMissingOrBlankLabel(string? actionLabel)
    {
        Assert.Empty(CreateDocument(actionLabel: actionLabel).Descendants("action"));
    }

    [Fact]
    public void Create_AddsCircularAppLogoOverrideForValidHttpsImage()
    {
        var image = Assert.Single(
            CreateDocument(imageUrl: "https://example.com/avatar.jpg").Descendants("image"));
        Assert.Equal("https://example.com/avatar.jpg", (string?)image.Attribute("src"));
        Assert.Equal("appLogoOverride", (string?)image.Attribute("placement"));
        Assert.Equal("circle", (string?)image.Attribute("hint-crop"));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("http://example.com/avatar.jpg")]
    [InlineData("file:///C:/Windows/System32/cmd.exe")]
    public void Create_OmitsAppLogoOverrideForMissingOrUnsafeImageUrl(string? imageUrl)
    {
        Assert.Empty(CreateDocument(imageUrl: imageUrl).Descendants("image"));
    }

    private static XDocument CreateDocument(
        string? actionLabel = "Open",
        string? actionUrl = "https://example.com/item",
        string? imageUrl = null)
    {
        var request = new ToastRequest(
            "Title", "Message", "App", imageUrl, actionLabel, actionUrl,
            Array.Empty<InboundNotification>());
        return XDocument.Parse(WindowsToastContentFactory.Create(request).GetContent());
    }
}
