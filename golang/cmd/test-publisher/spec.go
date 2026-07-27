// Command test-publisher is a dev-only tool that publishes synthetic
// notification events onto NATS so a running notification agent can be
// exercised without a real producer. It is a faithful Go port of the
// C# tool at tools/TestPublisher and the Rust port at rust/test-publisher.
package main

// PublishSpec holds every field needed to build and publish one or more
// notification event payloads. Optional string fields are *string so the
// zero value (nil) can be distinguished from an explicitly empty string,
// matching the Rust port's use of Option<String>.
type PublishSpec struct {
	UserID           string
	Title            string
	Message          string
	Secondary        *string
	NotificationType string
	Priority         string
	Count            int
	ImageURL         *string
	ImageShape       string
	ActionLabel      *string
	ActionURL        *string
	AggKey           *string
	DedupKey         *string
	Replaceable      bool
	DelayMs          int
	Messages         []string // nil means "not set"; distinct from an empty, non-nil slice
	Expect           *string
}

// strPtr is a small helper for taking the address of a string literal.
func strPtr(s string) *string { return &s }

// Defaults returns the legacy baseline spec — byte-compatible with the
// pre-scenario tool's default behavior.
func Defaults(userID string) PublishSpec {
	return PublishSpec{
		UserID:           userID,
		Title:            "Invoice ready",
		Message:          "Invoice INV-8492 is ready for review.",
		Secondary:        strPtr("TestPublisher"),
		NotificationType: "billing.invoice.ready",
		Priority:         "normal",
		Count:            1,
		ImageShape:       "circle",
		ActionLabel:      strPtr("View"),
		ActionURL:        strPtr("https://app.example.com/invoices/8492"),
	}
}

// ResolveMessages returns the explicit Messages list if set, otherwise
// Message repeated Count times.
func (s *PublishSpec) ResolveMessages() []string {
	if s.Messages != nil {
		return s.Messages
	}
	msgs := make([]string, s.Count)
	for i := range msgs {
		msgs[i] = s.Message
	}
	return msgs
}

// ApplyScenario mutates spec in place with one of the five named presets and
// reports whether name was recognized. On an unknown name, spec is left
// untouched. Field values are reproduced verbatim from the Rust reference
// (rust/test-publisher/src/spec.rs's apply_scenario) — do not paraphrase.
func ApplyScenario(spec *PublishSpec, name string) bool {
	switch name {
	case "presence":
		spec.Title = "Tony Redmond"
		spec.Message = "is now available"
		spec.Secondary = strPtr("Microsoft Teams")
		spec.NotificationType = "presence.available"
		spec.Priority = "critical"
		spec.ImageURL = strPtr("https://i.pravatar.cc/96?u=tony")
		spec.ImageShape = "circle"
		spec.ActionLabel = strPtr("Open chat")
		spec.ActionURL = strPtr("https://teams.example.com/chat/tony")
		spec.Expect = strPtr("1 avatar toast, 2 acks")
		return true
	case "invoice":
		spec.Title = "Invoice ready"
		spec.Message = "Invoice INV-8492 is ready for review."
		spec.Secondary = strPtr("Contoso Billing")
		spec.NotificationType = "billing.invoice.ready"
		spec.Priority = "normal"
		spec.ActionLabel = strPtr("View invoice")
		spec.ActionURL = strPtr("https://app.example.com/invoices/8492")
		spec.Expect = strPtr("1 toast after ~10s, 2 acks")
		return true
	case "progress":
		spec.Title = "Export job"
		spec.NotificationType = "job.progress"
		spec.AggKey = strPtr("job.progress")
		spec.Priority = "normal"
		spec.Replaceable = true
		spec.DelayMs = 100
		spec.Messages = []string{"10%", "60%", "90%"}
		spec.Expect = strPtr("after ~10s ONE toast showing 90%")
		return true
	case "batch":
		spec.Title = "Batch demo"
		spec.AggKey = strPtr("demo.batch")
		spec.Priority = "normal"
		spec.DelayMs = 100
		spec.Messages = []string{"first", "second", "third"}
		spec.Expect = strPtr("ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt")
		return true
	case "dedup":
		spec.Priority = "critical"
		spec.DedupKey = strPtr("dedup-demo")
		spec.Count = 3
		spec.Expect = strPtr("ONE toast, exactly 2 acks (duplicates dropped)")
		return true
	default:
		return false
	}
}
