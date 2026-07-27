package parser

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
)

// Exact example payload from design doc §7 / windows_desktop_notification_agent_core_nats_design.html
// (same fixture used by the C# and Rust reference tests).
const docExample = `{
  "schemaVersion": "1.0",
  "eventId": "evt-12345",
  "notificationType": "billing.invoice.ready",
  "target": { "userId": "u_7f92a845" },
  "content": {
    "title": "Invoice ready",
    "message": "Invoice INV-8492 is ready for review.",
    "secondaryText": "Contoso Billing"
  },
  "action": { "label": "View invoice", "url": "https://app.example.com/invoices/8492" },
  "classification": {
    "priority": "normal",
    "aggregationKey": "billing.invoice.ready",
    "deduplicationKey": "invoice.ready:8492",
    "replaceable": false
  },
  "timestamps": {
    "producerCreatedAt": "2026-07-15T08:30:00.100Z",
    "serverPublishedAt": "2026-07-15T08:30:00.150Z"
  }
}`

func mustParse(t *testing.T, payload string) *model.InboundNotification {
	t.Helper()
	n, err := Parse([]byte(payload))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	return n
}

func TestParsesDocExamplePayload(t *testing.T) {
	n := mustParse(t, docExample)

	if n.EventID != "evt-12345" {
		t.Errorf("EventID = %q, want evt-12345", n.EventID)
	}
	if n.UserID != "u_7f92a845" {
		t.Errorf("UserID = %q, want u_7f92a845", n.UserID)
	}
	if n.Title != "Invoice ready" {
		t.Errorf("Title = %q", n.Title)
	}
	if n.Message != "Invoice INV-8492 is ready for review." {
		t.Errorf("Message = %q", n.Message)
	}
	if n.SecondaryText != "Contoso Billing" {
		t.Errorf("SecondaryText = %q", n.SecondaryText)
	}
	if n.Action == nil {
		t.Fatal("Action = nil, want present")
	}
	if n.Action.Label != "View invoice" || n.Action.URL != "https://app.example.com/invoices/8492" {
		t.Errorf("Action = %+v", n.Action)
	}
	if n.Classification.Priority != model.PriorityNormal {
		t.Errorf("Priority = %v, want Normal", n.Classification.Priority)
	}
	if n.Classification.AggregationKey != "billing.invoice.ready" {
		t.Errorf("AggregationKey = %q", n.Classification.AggregationKey)
	}
	if n.Classification.DeduplicationKey != "invoice.ready:8492" {
		t.Errorf("DeduplicationKey = %q", n.Classification.DeduplicationKey)
	}
	if n.Classification.Replaceable {
		t.Error("Replaceable = true, want false")
	}
	wantProducer, _ := time.Parse(time.RFC3339Nano, "2026-07-15T08:30:00.100Z")
	if !n.ProducerCreatedAt.Equal(wantProducer) {
		t.Errorf("ProducerCreatedAt = %v, want %v", n.ProducerCreatedAt, wantProducer)
	}
	wantServer, _ := time.Parse(time.RFC3339Nano, "2026-07-15T08:30:00.150Z")
	if !n.ServerPublishedAt.Equal(wantServer) {
		t.Errorf("ServerPublishedAt = %v, want %v", n.ServerPublishedAt, wantServer)
	}
	if n.Image != nil {
		t.Errorf("Image = %+v, want nil (doc example has no image)", n.Image)
	}
}

func TestRejectsMissingOrBlankRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			"missing eventId",
			`{"target":{"userId":"u1"},"content":{"title":"t","message":"m"}}`,
		},
		{
			"missing target.userId (no target)",
			`{"eventId":"e1","content":{"title":"t","message":"m"}}`,
		},
		{
			"missing content.title",
			`{"eventId":"e1","target":{"userId":"u1"},"content":{"message":"m"}}`,
		},
		{
			"missing content.message",
			`{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t"}}`,
		},
		{
			"blank eventId",
			`{"eventId":"   ","target":{"userId":"u1"},"content":{"title":"t","message":"m"}}`,
		},
		{
			"blank target.userId",
			`{"eventId":"e1","target":{"userId":" "},"content":{"title":"t","message":"m"}}`,
		},
		{
			"blank content.title",
			`{"eventId":"e1","target":{"userId":"u1"},"content":{"title":" ","message":"m"}}`,
		},
		{
			"blank content.message",
			`{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t","message":""}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse([]byte(c.json)); err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", c.json)
			}
		})
	}
}

func TestRejectsPayloadOver32KiB(t *testing.T) {
	big := make([]byte, MaxPayloadBytes+1)
	for i := range big {
		big[i] = ' '
	}
	_, err := Parse(big)
	if err == nil {
		t.Fatal("expected error for payload over MaxPayloadBytes")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %q, want it to mention 'exceeds'", err.Error())
	}
}

func TestDepthBoundary16Allowed17Rejected(t *testing.T) {
	// depth 16 passes the depth gate and then deterministically fails the
	// eventId requirement -- proving the gate did not fire at exactly 16.
	d16 := strings.Repeat(`{"a":`, 16) + "1" + strings.Repeat("}", 16)
	_, err := Parse([]byte(d16))
	if err == nil || !strings.Contains(err.Error(), "eventId") {
		t.Fatalf("depth 16 must pass the depth gate, got: %v", err)
	}

	d17 := strings.Repeat(`{"a":`, 17) + "1" + strings.Repeat("}", 17)
	_, err = Parse([]byte(d17))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "depth") {
		t.Fatalf("depth 17 must be rejected by the depth gate, got: %v", err)
	}
}

func TestRejectsMalformedEmptyAndNullPayloads(t *testing.T) {
	for _, p := range [][]byte{[]byte("not json"), []byte(""), []byte("null")} {
		if _, err := Parse(p); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", p)
		}
	}
}

func TestMapsPriorityStrings(t *testing.T) {
	cases := []struct {
		priority string
		want     model.Priority
	}{
		{"critical", model.PriorityCritical},
		{"important", model.PriorityImportant},
		{"normal", model.PriorityNormal},
		{"garbage", model.PriorityNormal}, // unknown degrades to normal
		{"", model.PriorityNormal},        // missing defaults to normal
	}
	for _, c := range cases {
		json := fmt.Sprintf(
			`{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t","message":"m"},"classification":{"priority":%q}}`,
			c.priority,
		)
		n := mustParse(t, json)
		if n.Classification.Priority != c.want {
			t.Errorf("priority %q = %v, want %v", c.priority, n.Classification.Priority, c.want)
		}
	}
}

func TestAppliesDefaultsForMissingOptionalFields(t *testing.T) {
	json := `{"eventId":"e1","notificationType":"a.b",
		"target":{"userId":"u1"},"content":{"title":"t","message":"m"}}`
	n := mustParse(t, json)

	if n.Classification.DeduplicationKey != "e1" {
		t.Errorf("DeduplicationKey = %q, want eventId default 'e1'", n.Classification.DeduplicationKey)
	}
	if n.Classification.AggregationKey != "a.b" {
		t.Errorf("AggregationKey = %q, want notificationType default 'a.b'", n.Classification.AggregationKey)
	}
	if n.Classification.Priority != model.PriorityNormal {
		t.Errorf("Priority = %v, want Normal", n.Classification.Priority)
	}
	if n.Classification.Replaceable {
		t.Error("Replaceable = true, want false")
	}
	if n.Action != nil {
		t.Errorf("Action = %+v, want nil", n.Action)
	}
	if !n.ProducerCreatedAt.IsZero() {
		t.Errorf("ProducerCreatedAt = %v, want zero value", n.ProducerCreatedAt)
	}
	if !n.ServerPublishedAt.IsZero() {
		t.Errorf("ServerPublishedAt = %v, want zero value", n.ServerPublishedAt)
	}
}

func TestAggregationKeyFallsBackToUnknown(t *testing.T) {
	json := `{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t","message":"m"}}`
	n := mustParse(t, json)
	if n.Classification.AggregationKey != "unknown" {
		t.Errorf("AggregationKey = %q, want unknown", n.Classification.AggregationKey)
	}
}

func TestReplaceableDefaultsToFalse(t *testing.T) {
	json := `{"eventId":"e1","target":{"userId":"u1"},"content":{"title":"t","message":"m"}}`
	n := mustParse(t, json)
	if n.Classification.Replaceable {
		t.Error("Replaceable should default to false when absent")
	}
}

func TestAbsentImageIsNilAndDoesNotAffectRestOfEvent(t *testing.T) {
	n := mustParse(t, docExample)
	if n.Image != nil {
		t.Errorf("Image = %+v, want nil", n.Image)
	}
	// rest of the event should be unaffected (spot check a couple of fields)
	if n.EventID != "evt-12345" || n.Title != "Invoice ready" {
		t.Errorf("unrelated fields affected by absent image: %+v", n)
	}
}

func TestParsesImageWithDefaultCircleShape(t *testing.T) {
	json := `{"eventId":"e1","target":{"userId":"u1"},
		"content":{"title":"t","message":"m",
			"image":{"url":"https://cdn.example.com/a.jpg"}}}`
	n := mustParse(t, json)
	if n.Image == nil {
		t.Fatal("expected image present")
	}
	if n.Image.URL != "https://cdn.example.com/a.jpg" {
		t.Errorf("Image.URL = %q", n.Image.URL)
	}
	if n.Image.Shape != model.ImageShapeCircle {
		t.Errorf("Image.Shape = %v, want Circle (default)", n.Image.Shape)
	}
}

func TestParsesSquareShapeAndDefaultsUnknownToCircle(t *testing.T) {
	cases := []struct {
		shape string
		want  model.ImageShape
	}{
		{"square", model.ImageShapeSquare},
		{"SQUARE", model.ImageShapeSquare},
		{"hexagon", model.ImageShapeCircle},
	}
	for _, c := range cases {
		json := fmt.Sprintf(
			`{"eventId":"e1","target":{"userId":"u1"},
				"content":{"title":"t","message":"m",
					"image":{"url":"https://x.example/a.png","shape":%q}}}`,
			c.shape,
		)
		n := mustParse(t, json)
		if n.Image == nil {
			t.Fatalf("shape %q: expected image present", c.shape)
		}
		if n.Image.Shape != c.want {
			t.Errorf("shape %q = %v, want %v", c.shape, n.Image.Shape, c.want)
		}
	}
}

func TestInvalidImageDropsImageNotEvent(t *testing.T) {
	cases := []struct {
		name  string
		image string
	}{
		{"wrong scheme", `{"url":"http://insecure.example/a.jpg"}`},
		{"embedded credentials", `{"url":"https://user:password@cdn.example/a.jpg"}`},
		{"blank url", `{"url":""}`},
		{"no url", `{"shape":"circle"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			json := fmt.Sprintf(
				`{"eventId":"e1","target":{"userId":"u1"},
					"content":{"title":"t","message":"m","image":%s}}`,
				c.image,
			)
			n := mustParse(t, json) // event itself must still parse OK
			if n.Image != nil {
				t.Errorf("case %q: Image = %+v, want nil", c.name, n.Image)
			}
			if n.EventID != "e1" || n.Title != "t" || n.Message != "m" {
				t.Errorf("case %q: rest of event affected: %+v", c.name, n)
			}
		})
	}
}

func TestOversizeImageURLDropsImageNotEvent(t *testing.T) {
	url := "https://x.example/" + strings.Repeat("a", maxImageURLLength)
	json := fmt.Sprintf(
		`{"eventId":"e1","target":{"userId":"u1"},
			"content":{"title":"t","message":"m","image":{"url":%q}}}`,
		url,
	)
	n := mustParse(t, json)
	if n.Image != nil {
		t.Errorf("Image = %+v, want nil for oversize URL", n.Image)
	}
}
