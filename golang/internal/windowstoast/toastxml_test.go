package windowstoast

import (
	"strings"
	"testing"

	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

// baseRequest mirrors toast_xml.rs's tests::toast() fixture.
func baseRequest(image *model.Image) toast.ToastRequest {
	return toast.ToastRequest{
		Title:       "Tony Redmond",
		Message:     "is now available",
		Attribution: "Microsoft Teams",
		ActionLabel: "Open <chat>",
		ActionURL:   "https://teams.example/chat?a=1&b=2",
		Image:       image,
	}
}

func TestBuildToastXML_NoImageBuildsTodaysXML(t *testing.T) {
	xml := BuildToastXML(baseRequest(nil), "")

	if !strings.HasPrefix(xml, `<toast><visual><binding template="ToastGeneric">`) {
		t.Fatalf("unexpected prefix: %s", xml)
	}
	if !strings.Contains(xml, "<text>Tony Redmond</text>") {
		t.Fatalf("missing title text: %s", xml)
	}
	if strings.Contains(xml, "<image") {
		t.Fatalf("expected no image element: %s", xml)
	}
	if !strings.Contains(xml, "Open &lt;chat&gt;") {
		t.Fatalf("expected escaped action label: %s", xml)
	}
	if !strings.Contains(xml, "a=1&amp;b=2") {
		t.Fatalf("expected escaped action url: %s", xml)
	}
	if !strings.Contains(xml, `activationType="protocol"`) {
		t.Fatalf("expected protocol activation: %s", xml)
	}
}

func TestBuildToastXML_ValidHTTPSActionURLUnchangedByEscaping(t *testing.T) {
	xml := BuildToastXML(baseRequest(nil), "")
	if !strings.Contains(xml, `arguments="https://teams.example/chat?a=1&amp;b=2"`) {
		t.Fatalf("unexpected arguments attribute: %s", xml)
	}
}

func TestBuildToastXML_HTTPActionURLRejectedNoActionsElement(t *testing.T) {
	req := baseRequest(nil)
	req.ActionURL = "http://teams.example/chat"
	xml := BuildToastXML(req, "")
	if strings.Contains(xml, "<actions>") || strings.Contains(xml, "<action") {
		t.Fatalf("expected no actions element for http url: %s", xml)
	}
}

func TestBuildToastXML_UserinfoActionURLRejectedNoActionsElement(t *testing.T) {
	req := baseRequest(nil)
	req.ActionURL = "https://user:pass@teams.example/chat"
	xml := BuildToastXML(req, "")
	if strings.Contains(xml, "<actions>") || strings.Contains(xml, "<action") {
		t.Fatalf("expected no actions element for userinfo url: %s", xml)
	}
}

func TestBuildToastXML_MissingActionLabelOmitsActionsElement(t *testing.T) {
	req := baseRequest(nil)
	req.ActionLabel = ""
	xml := BuildToastXML(req, "")
	if strings.Contains(xml, "<actions>") {
		t.Fatalf("expected no actions element when label missing: %s", xml)
	}
}

func TestBuildToastXML_MissingActionURLOmitsActionsElement(t *testing.T) {
	req := baseRequest(nil)
	req.ActionURL = ""
	xml := BuildToastXML(req, "")
	if strings.Contains(xml, "<actions>") {
		t.Fatalf("expected no actions element when url missing: %s", xml)
	}
}

func TestBuildToastXML_CircleImageGetsAppLogoWithCrop(t *testing.T) {
	image := &model.Image{URL: "https://x/a.jpg", Shape: model.ImageShapeCircle}
	xml := BuildToastXML(baseRequest(image), "/tmp/cache/abc123")
	want := `<image placement="appLogoOverride" hint-crop="circle" src="file:///tmp/cache/abc123"/>`
	if !strings.Contains(xml, want) {
		t.Fatalf("expected %q in %s", want, xml)
	}
}

func TestBuildToastXML_SquareImageOmitsCropAttribute(t *testing.T) {
	image := &model.Image{URL: "https://x/a.jpg", Shape: model.ImageShapeSquare}
	xml := BuildToastXML(baseRequest(image), "/tmp/cache/abc123")
	want := `<image placement="appLogoOverride" src="file:///tmp/cache/abc123"/>`
	if !strings.Contains(xml, want) {
		t.Fatalf("expected %q in %s", want, xml)
	}
	if strings.Contains(xml, "hint-crop") {
		t.Fatalf("expected no hint-crop attribute: %s", xml)
	}
}

func TestBuildToastXML_ImageRefWithoutLocalPathRendersImageless(t *testing.T) {
	image := &model.Image{URL: "https://x/a.jpg", Shape: model.ImageShapeCircle}
	xml := BuildToastXML(baseRequest(image), "") // fetch failed
	if strings.Contains(xml, "<image") {
		t.Fatalf("expected no image element: %s", xml)
	}
}

func TestBuildToastXML_WindowsBackslashPathsBecomeForwardSlashFileURIs(t *testing.T) {
	image := &model.Image{URL: "https://x/a.jpg", Shape: model.ImageShapeSquare}
	xml := BuildToastXML(baseRequest(image), `C:\Users\u\AppData\Local\DesktopNotificationAgent\image-cache\abc`)
	want := `src="file:///C:/Users/u/AppData/Local/DesktopNotificationAgent/image-cache/abc"/>`
	if !strings.Contains(xml, want) {
		t.Fatalf("expected %q in %s", want, xml)
	}
}

func TestBuildToastXML_PathsWithSpacesArePercentEncoded(t *testing.T) {
	image := &model.Image{URL: "https://x/a.jpg", Shape: model.ImageShapeSquare}
	xml := BuildToastXML(baseRequest(image), `C:\Users\John Smith\AppData\Local\DesktopNotificationAgent\image-cache\abc`)
	want := `src="file:///C:/Users/John%20Smith/AppData/Local/DesktopNotificationAgent/image-cache/abc"/>`
	if !strings.Contains(xml, want) {
		t.Fatalf("expected %q in %s", want, xml)
	}
}

func TestBuildToastXML_AttributionOmittedWhenAbsent(t *testing.T) {
	req := baseRequest(nil)
	req.Attribution = ""
	xml := BuildToastXML(req, "")
	if strings.Contains(xml, `placement="attribution"`) {
		t.Fatalf("expected no attribution text: %s", xml)
	}
}

func TestXMLEscape(t *testing.T) {
	got := XMLEscape(`& < > " '`)
	want := "&amp; &lt; &gt; &quot; &apos;"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
