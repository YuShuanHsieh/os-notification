package toast_test

import (
	"strings"
	"testing"

	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

func event(id, title, message string) *model.InboundNotification {
	return &model.InboundNotification{
		EventID: id,
		Title:   title,
		Message: message,
		Classification: model.Classification{
			AggregationKey: "agg.key",
		},
	}
}

func TestFromSingle_ShortTitleAndMessageUnchanged(t *testing.T) {
	n := event("e1", "Title", "Message")
	got := toast.FromSingle(n)

	if got.Title != "Title" {
		t.Errorf("Title = %q, want %q", got.Title, "Title")
	}
	if got.Message != "Message" {
		t.Errorf("Message = %q, want %q", got.Message, "Message")
	}
}

func TestFromSingle_TruncatesLongTitleAndMessage(t *testing.T) {
	n := event("e1", strings.Repeat("T", 200), strings.Repeat("M", 600))
	got := toast.FromSingle(n)

	// Matches graphemetext.Truncate's own contract: keeps maxClusters-1 real
	// clusters plus a trailing ellipsis.
	wantTitle := strings.Repeat("T", toast.MaxTitleGraphemes-1) + "…"
	wantMessage := strings.Repeat("M", toast.MaxMessageGraphemes-1) + "…"

	if got.Title != wantTitle {
		t.Errorf("Title = %q, want %q", got.Title, wantTitle)
	}
	if got.Message != wantMessage {
		t.Errorf("Message = %q, want %q", got.Message, wantMessage)
	}
}

func TestFromSingle_MapsSecondaryTextImageAndAction(t *testing.T) {
	n := event("e1", "Title", "Message")
	n.SecondaryText = "App"
	n.Image = &model.Image{URL: "https://cdn.example.com/avatars/tony.jpg", Shape: model.ImageShapeCircle}
	n.Action = &model.Action{Label: "Open", URL: "https://example.com/x"}

	got := toast.FromSingle(n)

	if got.Attribution != "App" {
		t.Errorf("Attribution = %q, want %q", got.Attribution, "App")
	}
	if got.Image != n.Image {
		t.Errorf("Image = %v, want the same *model.Image pointer %v", got.Image, n.Image)
	}
	if got.ActionLabel != "Open" {
		t.Errorf("ActionLabel = %q, want %q", got.ActionLabel, "Open")
	}
	if got.ActionURL != "https://example.com/x" {
		t.Errorf("ActionURL = %q, want %q", got.ActionURL, "https://example.com/x")
	}
	if len(got.Sources) != 1 || got.Sources[0] != n {
		t.Errorf("Sources = %v, want [n]", got.Sources)
	}
}

func TestFromSingle_NilActionYieldsEmptyLabelAndURL(t *testing.T) {
	n := event("e1", "Title", "Message")
	n.Action = nil

	got := toast.FromSingle(n)

	if got.ActionLabel != "" {
		t.Errorf("ActionLabel = %q, want empty", got.ActionLabel)
	}
	if got.ActionURL != "" {
		t.Errorf("ActionURL = %q, want empty", got.ActionURL)
	}
}

func TestFromSingle_NonHTTPSActionURLAndImagePassThroughUnchanged(t *testing.T) {
	// This package performs no URL policy enforcement: HTTPS-only validation
	// is a Windows-renderer concern, not a ToastContentFactory concern.
	n := event("e1", "Title", "Message")
	n.Image = &model.Image{URL: "http://cdn.example.com/avatar.jpg"}
	n.Action = &model.Action{Label: "Open", URL: "http://example.com/x"}

	got := toast.FromSingle(n)

	if got.Image.URL != "http://cdn.example.com/avatar.jpg" {
		t.Errorf("Image.URL = %q, want unchanged http:// URL", got.Image.URL)
	}
	if got.ActionURL != "http://example.com/x" {
		t.Errorf("ActionURL = %q, want unchanged http:// URL", got.ActionURL)
	}
}

func TestFromBatch_EmptySlicePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("FromBatch(nil) did not panic, want panic on empty batch")
		}
	}()
	toast.FromBatch(nil)
}

func TestFromBatch_OneEventMatchesFromSingle(t *testing.T) {
	n := event("e1", "Title", "Message")
	n.SecondaryText = "App"
	n.Action = &model.Action{Label: "Open", URL: "https://example.com/x"}

	fromSingle := toast.FromSingle(n)
	fromBatch := toast.FromBatch([]*model.InboundNotification{n})

	if fromBatch.Title != fromSingle.Title ||
		fromBatch.Message != fromSingle.Message ||
		fromBatch.Attribution != fromSingle.Attribution ||
		fromBatch.ActionLabel != fromSingle.ActionLabel ||
		fromBatch.ActionURL != fromSingle.ActionURL {
		t.Errorf("FromBatch(single) = %+v, want identical to FromSingle = %+v", fromBatch, fromSingle)
	}
	if len(fromBatch.Sources) != 1 || fromBatch.Sources[0] != n {
		t.Errorf("Sources = %v, want [n]", fromBatch.Sources)
	}
}

func TestFromBatch_ThreeEventsSummarizeUsingLastAsLatest(t *testing.T) {
	e1 := event("e1", "T1", "first")
	e2 := event("e2", "T2", "second")
	e3 := event("e3", "T3", "third")
	e3.Classification.AggregationKey = "agg.key" // shared bucket key
	e3.SecondaryText = "App3"
	e3.Image = &model.Image{URL: "https://cdn.example.com/third.jpg"}
	e3.Action = &model.Action{Label: "OpenThird", URL: "https://example.com/third"}

	// First two events carry different attribution/image/action so we can
	// prove the batch takes these fields from the LAST element, not the first.
	e1.SecondaryText = "App1"
	e1.Image = &model.Image{URL: "https://cdn.example.com/first.jpg"}
	e1.Action = &model.Action{Label: "OpenFirst", URL: "https://example.com/first"}

	batch := []*model.InboundNotification{e1, e2, e3}
	got := toast.FromBatch(batch)

	wantTitle := "3 notifications — agg.key"
	if got.Title != wantTitle {
		t.Errorf("Title = %q, want %q", got.Title, wantTitle)
	}
	if !strings.HasPrefix(got.Message, "Latest: ") {
		t.Errorf("Message = %q, want prefix %q", got.Message, "Latest: ")
	}
	if !strings.Contains(got.Message, "third") {
		t.Errorf("Message = %q, want to contain last event's message %q", got.Message, "third")
	}

	if got.Attribution != "App3" {
		t.Errorf("Attribution = %q, want %q (from latest)", got.Attribution, "App3")
	}
	if got.Image != e3.Image {
		t.Errorf("Image = %v, want latest's image %v", got.Image, e3.Image)
	}
	if got.ActionLabel != "OpenThird" {
		t.Errorf("ActionLabel = %q, want %q (from latest)", got.ActionLabel, "OpenThird")
	}
	if got.ActionURL != "https://example.com/third" {
		t.Errorf("ActionURL = %q, want %q (from latest)", got.ActionURL, "https://example.com/third")
	}

	if len(got.Sources) != 3 {
		t.Fatalf("Sources has %d elements, want 3", len(got.Sources))
	}
	if got.Sources[0] != e1 || got.Sources[1] != e2 || got.Sources[2] != e3 {
		t.Errorf("Sources = %v, want [e1, e2, e3] in original order", got.Sources)
	}
}

func TestFromBatch_TitleAndMessageAreGraphemeTruncated(t *testing.T) {
	e1 := event("e1", "T1", "first")
	e2 := event("e2", "T2", "second")
	e3 := event("e3", strings.Repeat("T", 10), strings.Repeat("M", 600))
	e3.Classification.AggregationKey = strings.Repeat("k", 200)

	got := toast.FromBatch([]*model.InboundNotification{e1, e2, e3})

	if strings.Count(got.Title, "…") != 1 {
		t.Errorf("Title = %q, want it grapheme-truncated with an ellipsis", got.Title)
	}
	if !strings.HasSuffix(got.Message, "…") {
		t.Errorf("Message = %q, want it grapheme-truncated with a trailing ellipsis", got.Message)
	}
}

func TestFromBatch_NonHTTPSActionURLAndImagePassThroughUnchanged(t *testing.T) {
	e1 := event("e1", "T1", "first")
	e2 := event("e2", "T2", "second")
	e2.Image = &model.Image{URL: "http://cdn.example.com/second.jpg"}
	e2.Action = &model.Action{Label: "Open", URL: "http://example.com/x"}

	got := toast.FromBatch([]*model.InboundNotification{e1, e2})

	if got.Image.URL != "http://cdn.example.com/second.jpg" {
		t.Errorf("Image.URL = %q, want unchanged http:// URL", got.Image.URL)
	}
	if got.ActionURL != "http://example.com/x" {
		t.Errorf("ActionURL = %q, want unchanged http:// URL", got.ActionURL)
	}
}
