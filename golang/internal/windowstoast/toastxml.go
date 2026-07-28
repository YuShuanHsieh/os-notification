// Package windowstoast builds the toast notification XML consumed by
// Windows.UI.Notifications.ToastNotification, mirroring
// rust/notify-agent-core/src/toast_xml.rs byte-for-byte in shape. It is a
// pure, cross-platform-testable string builder: the only Windows-specific
// part of rendering — invoking PowerShell to actually submit the XML — lives
// in cmd/notify-agent-windows, not here.
package windowstoast

import (
	"fmt"
	"strings"

	"github.com/YuShuanHsieh/os-notification/golang/internal/httpsurl"
	"github.com/YuShuanHsieh/os-notification/golang/internal/model"
	"github.com/YuShuanHsieh/os-notification/golang/internal/toast"
)

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

// XMLEscape escapes the five reserved XML characters in s.
func XMLEscape(s string) string {
	return xmlEscaper.Replace(s)
}

// fileURI builds a file:/// URI from a local path with forward slashes and
// percent-encoded segments; a leading '/' (unix paths) is not doubled. This
// mirrors toast_xml.rs's file_uri, tuned for the two path shapes this
// codebase produces (unix absolute, Windows drive-letter), not general URIs.
func fileURI(path string) string {
	p := strings.ReplaceAll(path, `\`, "/")
	p = strings.TrimPrefix(p, "/")

	var b strings.Builder
	b.WriteString("file:///")
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == '/', c == ':':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// BuildToastXML builds the toast XML for req. cachedImagePath is the local
// on-disk path of req.Image, already resolved by the image cache — pass ""
// when there is no image or the fetch failed (the OS ignores remote URLs for
// unpackaged apps, so a failed fetch renders imageless rather than erroring).
//
// Shape: <toast><visual><binding template="ToastGeneric">{image}<text>title</text>
// <text>message</text>{attribution}</binding></visual>{actions}</toast>, where
// {image} is present only when both req.Image and cachedImagePath are set,
// {attribution} is present only when req.Attribution is non-empty, and
// {actions} is present only when both ActionLabel and ActionURL are
// non-empty AND ActionURL passes httpsurl.Valid — an invalid or partial
// action never fails the toast, it just omits the button.
func BuildToastXML(req toast.ToastRequest, cachedImagePath string) string {
	image := ""
	if cachedImagePath != "" && req.Image != nil {
		crop := ""
		if req.Image.Shape == model.ImageShapeCircle {
			crop = ` hint-crop="circle"`
		}
		image = fmt.Sprintf(`<image placement="appLogoOverride"%s src="%s"/>`,
			crop, XMLEscape(fileURI(cachedImagePath)))
	}

	attribution := ""
	if req.Attribution != "" {
		attribution = fmt.Sprintf(`<text placement="attribution">%s</text>`, XMLEscape(req.Attribution))
	}

	actions := ""
	if req.ActionLabel != "" && req.ActionURL != "" && httpsurl.Valid(req.ActionURL) {
		actions = fmt.Sprintf(
			`<actions><action content="%s" arguments="%s" activationType="protocol"/></actions>`,
			XMLEscape(req.ActionLabel), XMLEscape(req.ActionURL),
		)
	}

	return fmt.Sprintf(
		`<toast><visual><binding template="ToastGeneric">%s<text>%s</text><text>%s</text>%s</binding></visual>%s</toast>`,
		image, XMLEscape(req.Title), XMLEscape(req.Message), attribution, actions,
	)
}
