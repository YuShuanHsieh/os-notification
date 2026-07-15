using NotificationAgent.Core.Models;
using NotificationAgent.Core.Rendering;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class ToastContentFactoryTests
{
    internal static InboundNotification Event(
        string id = "e1", string title = "Title", string message = "Message",
        EventPriority priority = EventPriority.Normal, string aggKey = "agg.key",
        string? dedupKey = null, bool replaceable = false,
        string? actionLabel = "Open", string? actionUrl = "https://example.com/x") =>
        new(id, "u1", title, message, "App", actionLabel, actionUrl, priority,
            aggKey, dedupKey ?? id, replaceable, null, null,
            DateTimeOffset.Parse("2026-07-15T08:30:00.190Z"));

    [Fact]
    public void Single_event_maps_fields_directly()
    {
        var n = Event();
        var toast = ToastContentFactory.FromSingle(n);

        Assert.Equal("Title", toast.Title);
        Assert.Equal("Message", toast.Message);
        Assert.Equal("App", toast.Attribution);
        Assert.Equal("Open", toast.ActionLabel);
        Assert.Equal("https://example.com/x", toast.ActionUrl);
        Assert.Equal(new[] { n }, toast.Sources);
    }

    [Fact]
    public void Single_event_truncates_title_to_120_and_message_to_500_graphemes()
    {
        var toast = ToastContentFactory.FromSingle(
            Event(title: new string('T', 200), message: new string('M', 600)));

        Assert.Equal(120, new System.Globalization.StringInfo(toast.Title).LengthInTextElements);
        Assert.EndsWith("…", toast.Title);
        Assert.Equal(500, new System.Globalization.StringInfo(toast.Message).LengthInTextElements);
        Assert.EndsWith("…", toast.Message);
    }

    [Fact]
    public void Batch_of_one_behaves_like_single()
    {
        // Field-by-field: ToastRequest holds an IReadOnlyList, so record equality
        // is reference-based on that member and can't be used here.
        var n = Event();
        var toast = ToastContentFactory.FromBatch(new[] { n });
        Assert.Equal("Title", toast.Title);
        Assert.Equal("Message", toast.Message);
        Assert.Same(n, Assert.Single(toast.Sources));
    }

    [Fact]
    public void Batch_summarizes_count_and_latest_event()
    {
        var batch = new[] { Event("e1", message: "first"), Event("e2", message: "second"),
                            Event("e3", message: "third") };
        var toast = ToastContentFactory.FromBatch(batch);

        Assert.Equal("3 notifications — agg.key", toast.Title);
        Assert.Equal("Latest: third", toast.Message);
        Assert.Equal(3, toast.Sources.Count);
        Assert.Equal("Open", toast.ActionLabel); // action of the latest event
    }

    [Fact]
    public void Empty_batch_throws()
    {
        Assert.Throws<ArgumentException>(() =>
            ToastContentFactory.FromBatch(Array.Empty<InboundNotification>()));
    }
}
