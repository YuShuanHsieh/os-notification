// Package toast builds renderer-neutral toast content from one or more
// inbound notifications, mirroring ToastContentFactory.cs (C#) and
// toast.rs (Rust) elsewhere in this repository.
package toast

import (
	"context"
	"fmt"
	"time"

	"github.com/YuShuanHsieh/os-notification/golang/internal/graphemetext"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
)

// MaxTitleGraphemes and MaxMessageGraphemes bound the title and message of a
// ToastRequest by extended grapheme cluster count, matching the C#/Rust
// reference implementations exactly.
const (
	MaxTitleGraphemes   = 120
	MaxMessageGraphemes = 500
)

// ToastRequest is a renderer-neutral, ready-to-show toast. Sources lists
// every source event this toast represents, so the caller can acknowledge
// each one as submitted_to_windows after a successful Show.
type ToastRequest struct {
	Title       string
	Message     string
	Attribution string // "" if absent
	Image       *model.Image
	ActionLabel string // "" if absent
	ActionURL   string // "" if absent
	Sources     []*model.InboundNotification
}

// Renderer submits a ToastRequest to the OS (or a console/dev stand-in) and
// returns the timestamp at which it was successfully submitted.
type Renderer interface {
	Show(ctx context.Context, req ToastRequest) (time.Time, error)
}

// FromSingle builds a ToastRequest for exactly one event. Title and Message
// are grapheme-truncated; every other field is passed through verbatim,
// including any non-HTTPS URLs — HTTPS enforcement is a Windows-renderer
// concern, not this package's.
func FromSingle(n *model.InboundNotification) ToastRequest {
	actionLabel, actionURL := actionFields(n)
	return ToastRequest{
		Title:       graphemetext.Truncate(n.Title, MaxTitleGraphemes),
		Message:     graphemetext.Truncate(n.Message, MaxMessageGraphemes),
		Attribution: n.SecondaryText,
		Image:       n.Image,
		ActionLabel: actionLabel,
		ActionURL:   actionURL,
		Sources:     []*model.InboundNotification{n},
	}
}

// FromBatch builds a ToastRequest from one or more events sharing an
// aggregation bucket. Panics if batch is empty (mirrors the C# reference's
// ArgumentException / Rust's assert! on an empty batch — this is a
// programmer-error case that should never happen given a correctly-written
// caller, not a runtime condition to handle gracefully). Delegates to
// FromSingle for a batch of exactly 1. For a batch of >1, the batch is
// expected in arrival order and the LAST element is treated as "latest":
// title = "<count> notifications — <latest's AggregationKey>", message =
// "Latest: <latest's Message>" (both grapheme-truncated the same as
// FromSingle), Attribution/Image/ActionLabel/ActionURL taken from latest,
// Sources = the full batch in order.
func FromBatch(batch []*model.InboundNotification) ToastRequest {
	if len(batch) == 0 {
		panic("toast: FromBatch requires a non-empty batch")
	}

	if len(batch) == 1 {
		return FromSingle(batch[0])
	}

	latest := batch[len(batch)-1]
	actionLabel, actionURL := actionFields(latest)

	title := fmt.Sprintf("%d notifications — %s", len(batch), latest.Classification.AggregationKey)
	message := fmt.Sprintf("Latest: %s", latest.Message)

	sources := make([]*model.InboundNotification, len(batch))
	copy(sources, batch)

	return ToastRequest{
		Title:       graphemetext.Truncate(title, MaxTitleGraphemes),
		Message:     graphemetext.Truncate(message, MaxMessageGraphemes),
		Attribution: latest.SecondaryText,
		Image:       latest.Image,
		ActionLabel: actionLabel,
		ActionURL:   actionURL,
		Sources:     sources,
	}
}

// actionFields derives ActionLabel/ActionURL from n.Action, returning
// ("", "") when no action is present.
func actionFields(n *model.InboundNotification) (label, url string) {
	if n.Action == nil {
		return "", ""
	}
	return n.Action.Label, n.Action.URL
}
