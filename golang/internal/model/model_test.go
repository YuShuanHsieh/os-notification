package model

import "testing"

func TestParsePriority(t *testing.T) {
	cases := []struct {
		in   string
		want Priority
	}{
		{"critical", PriorityCritical},
		{"important", PriorityImportant},
		{"normal", PriorityNormal},
		{"", PriorityNormal},
		{"unrecognized", PriorityNormal},
	}
	for _, c := range cases {
		if got := ParsePriority(c.in); got != c.want {
			t.Errorf("ParsePriority(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPriorityString(t *testing.T) {
	cases := []struct {
		in   Priority
		want string
	}{
		{PriorityCritical, "critical"},
		{PriorityImportant, "important"},
		{PriorityNormal, "normal"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Priority(%d).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseImageShape(t *testing.T) {
	if got := ParseImageShape("square"); got != ImageShapeSquare {
		t.Errorf("ParseImageShape(square) = %v, want ImageShapeSquare", got)
	}
	if got := ParseImageShape("circle"); got != ImageShapeCircle {
		t.Errorf("ParseImageShape(circle) = %v, want ImageShapeCircle", got)
	}
	if got := ParseImageShape("unknown"); got != ImageShapeCircle {
		t.Errorf("ParseImageShape(unknown) = %v, want ImageShapeCircle (default)", got)
	}
}
