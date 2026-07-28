package main

import (
	"reflect"
	"testing"
)

func TestEmptyArgsIsAnError(t *testing.T) {
	_, err := parseArgs(nil)
	requireErr(t, err, "first argument must be <userId>")
}

func TestFlagAsFirstArgIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"--title", "x"})
	requireErr(t, err, "first argument must be <userId>")
}

func TestBareUserIDUsesDefaults(t *testing.T) {
	spec, err := parseArgs([]string{"u1"})
	requireOK(t, err)
	want := Defaults("u1")
	if !reflect.DeepEqual(spec, want) {
		t.Errorf("got %+v, want %+v", spec, want)
	}
}

func TestLegacyPositionalsFillTitleMessagePriorityCountImageURL(t *testing.T) {
	spec, err := parseArgs([]string{"u1", "T", "M", "critical", "3", "https://img"})
	requireOK(t, err)
	if spec.Title != "T" || spec.Message != "M" || spec.Priority != "critical" || spec.Count != 3 {
		t.Errorf("got %+v", spec)
	}
	if spec.ImageURL == nil || *spec.ImageURL != "https://img" {
		t.Errorf("ImageURL: got %v", spec.ImageURL)
	}
}

func TestTooManyLegacyPositionalsIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "T", "M", "critical", "3", "https://img", "extra"})
	requireErr(t, err, "too many legacy positional arguments")
}

func TestInvalidLegacyCountIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "T", "M", "critical", "zero"})
	requireErr(t, err, "count must be a positive integer")

	_, err = parseArgs([]string{"u1", "T", "M", "critical", "0"})
	requireErr(t, err, "count must be a positive integer")
}

func TestScenarioAppliesPresetFields(t *testing.T) {
	spec, err := parseArgs([]string{"u1", "--scenario", "invoice"})
	requireOK(t, err)
	expected := Defaults("u1")
	ApplyScenario(&expected, "invoice")
	if !reflect.DeepEqual(spec, expected) {
		t.Errorf("got %+v, want %+v", spec, expected)
	}
}

func TestUnknownScenarioIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--scenario", "nope"})
	requireErr(t, err, "unknown scenario 'nope' (presence|invoice|progress|batch|dedup)")
}

func TestScenarioCombinedWithLegacyPositionalIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "T", "--scenario", "invoice"})
	requireErr(t, err, "--scenario cannot be combined with legacy positional arguments")
}

func TestFlagsOverrideScenarioFields(t *testing.T) {
	spec, err := parseArgs([]string{"u1", "--scenario", "presence", "--priority", "normal"})
	requireOK(t, err)
	if spec.Priority != "normal" {
		t.Errorf("Priority: got %q", spec.Priority)
	}
	if spec.Title != "Tony Redmond" { // untouched preset field survives
		t.Errorf("Title: got %q", spec.Title)
	}
}

func TestMessageFlagClearsMessagesList(t *testing.T) {
	spec, err := parseArgs([]string{"u1", "--scenario", "progress", "--message", "custom"})
	requireOK(t, err)
	if spec.Message != "custom" {
		t.Errorf("Message: got %q", spec.Message)
	}
	if spec.Messages != nil {
		t.Errorf("Messages: got %v, want nil", spec.Messages)
	}
}

func TestCountFlagClearsMessagesList(t *testing.T) {
	spec, err := parseArgs([]string{"u1", "--scenario", "batch", "--count", "2"})
	requireOK(t, err)
	if spec.Count != 2 {
		t.Errorf("Count: got %d", spec.Count)
	}
	if spec.Messages != nil {
		t.Errorf("Messages: got %v, want nil", spec.Messages)
	}
}

func TestInvalidCountFlagIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--count", "0"})
	requireErr(t, err, "--count must be a positive integer")
}

func TestImageShapeFlagValidatesValue(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--image-shape", "hexagon"})
	requireErr(t, err, "--image-shape must be circle or square")

	spec, err := parseArgs([]string{"u1", "--image-shape", "square"})
	requireOK(t, err)
	if spec.ImageShape != "square" {
		t.Errorf("ImageShape: got %q", spec.ImageShape)
	}
}

func TestReplaceableFlagNeedsNoValue(t *testing.T) {
	spec, err := parseArgs([]string{"u1", "--replaceable"})
	requireOK(t, err)
	if !spec.Replaceable {
		t.Error("Replaceable: got false, want true")
	}
}

func TestDelayMsFlagValidatesValue(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--delay-ms", "-1"})
	requireErr(t, err, "--delay-ms must be a non-negative integer")

	spec, err := parseArgs([]string{"u1", "--delay-ms", "250"})
	requireOK(t, err)
	if spec.DelayMs != 250 {
		t.Errorf("DelayMs: got %d", spec.DelayMs)
	}
}

func TestUnknownFlagIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--nope"})
	requireErr(t, err, "unknown flag '--nope'")
}

func TestFlagMissingValueIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--title"})
	requireErr(t, err, "--title needs a value")
}

func TestCountBeyondI32RangeIsAnError(t *testing.T) {
	_, err := parseArgs([]string{"u1", "--count", "4294967297"})
	requireErr(t, err, "--count must be a positive integer")
}

func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}
