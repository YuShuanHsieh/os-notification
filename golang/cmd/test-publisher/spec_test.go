package main

import (
	"reflect"
	"testing"
)

func strEq(t *testing.T, got *string, want string, field string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: got nil, want %q", field, want)
	}
	if *got != want {
		t.Errorf("%s: got %q, want %q", field, *got, want)
	}
}

func strNil(t *testing.T, got *string, field string) {
	t.Helper()
	if got != nil {
		t.Errorf("%s: got %q, want nil", field, *got)
	}
}

func TestDefaultsMatchTheLegacyBaseline(t *testing.T) {
	spec := Defaults("u1")
	if spec.UserID != "u1" {
		t.Errorf("UserID: got %q, want u1", spec.UserID)
	}
	if spec.Title != "Invoice ready" {
		t.Errorf("Title: got %q", spec.Title)
	}
	if spec.Message != "Invoice INV-8492 is ready for review." {
		t.Errorf("Message: got %q", spec.Message)
	}
	strEq(t, spec.Secondary, "TestPublisher", "Secondary")
	if spec.NotificationType != "billing.invoice.ready" {
		t.Errorf("NotificationType: got %q", spec.NotificationType)
	}
	if spec.Priority != "normal" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	if spec.Count != 1 {
		t.Errorf("Count: got %d, want 1", spec.Count)
	}
	strNil(t, spec.ImageURL, "ImageURL")
	if spec.ImageShape != "circle" {
		t.Errorf("ImageShape: got %q", spec.ImageShape)
	}
	strEq(t, spec.ActionLabel, "View", "ActionLabel")
	strEq(t, spec.ActionURL, "https://app.example.com/invoices/8492", "ActionURL")
	strNil(t, spec.AggKey, "AggKey")
	strNil(t, spec.DedupKey, "DedupKey")
	if spec.Replaceable {
		t.Error("Replaceable: got true, want false")
	}
	if spec.DelayMs != 0 {
		t.Errorf("DelayMs: got %d, want 0", spec.DelayMs)
	}
	if spec.Messages != nil {
		t.Errorf("Messages: got %v, want nil", spec.Messages)
	}
	strNil(t, spec.Expect, "Expect")
}

func TestUnknownScenarioReturnsFalseAndLeavesSpecUntouched(t *testing.T) {
	spec := Defaults("u1")
	before := spec
	if ApplyScenario(&spec, "not-a-scenario") {
		t.Fatal("expected ApplyScenario to return false for unknown scenario")
	}
	if !reflect.DeepEqual(spec, before) {
		t.Errorf("spec mutated: got %+v, want %+v", spec, before)
	}
}

func TestPresenceScenario(t *testing.T) {
	spec := Defaults("u1")
	if !ApplyScenario(&spec, "presence") {
		t.Fatal("expected true")
	}
	if spec.Title != "Tony Redmond" {
		t.Errorf("Title: got %q", spec.Title)
	}
	if spec.Message != "is now available" {
		t.Errorf("Message: got %q", spec.Message)
	}
	strEq(t, spec.Secondary, "Microsoft Teams", "Secondary")
	if spec.NotificationType != "presence.available" {
		t.Errorf("NotificationType: got %q", spec.NotificationType)
	}
	if spec.Priority != "critical" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	strEq(t, spec.ImageURL, "https://i.pravatar.cc/96?u=tony", "ImageURL")
	if spec.ImageShape != "circle" {
		t.Errorf("ImageShape: got %q", spec.ImageShape)
	}
	strEq(t, spec.ActionLabel, "Open chat", "ActionLabel")
	strEq(t, spec.ActionURL, "https://teams.example.com/chat/tony", "ActionURL")
	strEq(t, spec.Expect, "1 avatar toast, 2 acks", "Expect")
}

func TestInvoiceScenario(t *testing.T) {
	spec := Defaults("u1")
	if !ApplyScenario(&spec, "invoice") {
		t.Fatal("expected true")
	}
	if spec.Title != "Invoice ready" {
		t.Errorf("Title: got %q", spec.Title)
	}
	if spec.Message != "Invoice INV-8492 is ready for review." {
		t.Errorf("Message: got %q", spec.Message)
	}
	strEq(t, spec.Secondary, "Contoso Billing", "Secondary")
	if spec.NotificationType != "billing.invoice.ready" {
		t.Errorf("NotificationType: got %q", spec.NotificationType)
	}
	if spec.Priority != "normal" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	strEq(t, spec.ActionLabel, "View invoice", "ActionLabel")
	strEq(t, spec.ActionURL, "https://app.example.com/invoices/8492", "ActionURL")
	strEq(t, spec.Expect, "1 toast after ~10s, 2 acks", "Expect")
}

func TestProgressScenarioLeavesUntouchedFieldsAtTheirDefaults(t *testing.T) {
	spec := Defaults("u1")
	if !ApplyScenario(&spec, "progress") {
		t.Fatal("expected true")
	}
	if spec.Title != "Export job" {
		t.Errorf("Title: got %q", spec.Title)
	}
	if spec.NotificationType != "job.progress" {
		t.Errorf("NotificationType: got %q", spec.NotificationType)
	}
	strEq(t, spec.AggKey, "job.progress", "AggKey")
	if spec.Priority != "normal" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	if !spec.Replaceable {
		t.Error("Replaceable: got false, want true")
	}
	if spec.DelayMs != 100 {
		t.Errorf("DelayMs: got %d, want 100", spec.DelayMs)
	}
	if !reflect.DeepEqual(spec.Messages, []string{"10%", "60%", "90%"}) {
		t.Errorf("Messages: got %v", spec.Messages)
	}
	strEq(t, spec.Expect, "after ~10s ONE toast showing 90%", "Expect")
	// progress doesn't touch these — they stay at Defaults() values.
	strEq(t, spec.ActionLabel, "View", "ActionLabel")
	strEq(t, spec.ActionURL, "https://app.example.com/invoices/8492", "ActionURL")
	strEq(t, spec.Secondary, "TestPublisher", "Secondary")
}

func TestBatchScenarioLeavesUntouchedFieldsAtTheirDefaults(t *testing.T) {
	spec := Defaults("u1")
	if !ApplyScenario(&spec, "batch") {
		t.Fatal("expected true")
	}
	if spec.Title != "Batch demo" {
		t.Errorf("Title: got %q", spec.Title)
	}
	strEq(t, spec.AggKey, "demo.batch", "AggKey")
	if spec.Priority != "normal" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	if spec.DelayMs != 100 {
		t.Errorf("DelayMs: got %d, want 100", spec.DelayMs)
	}
	if !reflect.DeepEqual(spec.Messages, []string{"first", "second", "third"}) {
		t.Errorf("Messages: got %v", spec.Messages)
	}
	strEq(t, spec.Expect, "ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt", "Expect")
	// batch doesn't touch these — they stay at Defaults() values.
	if spec.NotificationType != "billing.invoice.ready" {
		t.Errorf("NotificationType: got %q", spec.NotificationType)
	}
	strEq(t, spec.ActionLabel, "View", "ActionLabel")
}

func TestDedupScenarioLeavesUntouchedFieldsAtTheirDefaults(t *testing.T) {
	spec := Defaults("u1")
	if !ApplyScenario(&spec, "dedup") {
		t.Fatal("expected true")
	}
	if spec.Priority != "critical" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	strEq(t, spec.DedupKey, "dedup-demo", "DedupKey")
	if spec.Count != 3 {
		t.Errorf("Count: got %d, want 3", spec.Count)
	}
	strEq(t, spec.Expect, "ONE toast, exactly 2 acks (duplicates dropped)", "Expect")
	// dedup doesn't touch these — they stay at Defaults() values.
	if spec.Title != "Invoice ready" {
		t.Errorf("Title: got %q", spec.Title)
	}
	if spec.NotificationType != "billing.invoice.ready" {
		t.Errorf("NotificationType: got %q", spec.NotificationType)
	}
}

func TestResolveMessagesUsesMessagesListWhenSet(t *testing.T) {
	spec := Defaults("u1")
	spec.Messages = []string{"a", "b"}
	spec.Count = 5 // should be ignored — messages list wins
	got := spec.ResolveMessages()
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v", got)
	}
}

func TestResolveMessagesReplicatesMessageByCountWhenNoMessagesList(t *testing.T) {
	spec := Defaults("u1")
	spec.Message = "hi"
	spec.Count = 3
	got := spec.ResolveMessages()
	if !reflect.DeepEqual(got, []string{"hi", "hi", "hi"}) {
		t.Errorf("got %v", got)
	}
}
