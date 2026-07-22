use std::path::Path;

use crate::model::ImageShape;
use crate::toast::ToastRequest;

pub fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

/// file:/// URI with forward slashes and percent-encoded segments; a leading
/// '/' (unix paths) is not doubled. Tuned for the two path shapes this
/// codebase produces (unix absolute, windows drive-letter), not general URIs.
fn file_uri(path: &Path) -> String {
    let p = path.display().to_string().replace('\\', "/");
    let p = p.strip_prefix('/').unwrap_or(&p);
    let mut encoded = String::with_capacity(p.len());
    for b in p.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9'
            | b'-' | b'.' | b'_' | b'~' | b'/' | b':' => encoded.push(b as char),
            _ => encoded.push_str(&format!("%{b:02X}")),
        }
    }
    format!("file:///{encoded}")
}

/// Toast XML per design §7 budget: ≤3 texts, 1 button, plus the separate
/// appLogoOverride image slot (design 2026-07-22). image_path is the LOCAL
/// cached file — the OS ignores remote URLs for unpackaged apps, so a failed
/// fetch (None) renders imageless.
pub fn build_toast_xml(toast: &ToastRequest, image_path: Option<&Path>) -> String {
    let image = match (image_path, &toast.image) {
        (Some(path), Some(image_ref)) => {
            let crop = match image_ref.shape {
                ImageShape::Circle => r#" hint-crop="circle""#,
                ImageShape::Square => "",
            };
            format!(
                r#"<image placement="appLogoOverride"{crop} src="{}"/>"#,
                xml_escape(&file_uri(path))
            )
        }
        _ => String::new(),
    };
    let attribution = toast
        .attribution
        .as_deref()
        .map(|a| format!(r#"<text placement="attribution">{}</text>"#, xml_escape(a)))
        .unwrap_or_default();
    let actions = match (&toast.action_label, &toast.action_url) {
        (Some(label), Some(url)) => format!(
            r#"<actions><action content="{}" arguments="{}" activationType="foreground"/></actions>"#,
            xml_escape(label),
            xml_escape(url)
        ),
        _ => String::new(),
    };
    format!(
        r#"<toast><visual><binding template="ToastGeneric">{image}<text>{}</text><text>{}</text>{attribution}</binding></visual>{actions}</toast>"#,
        xml_escape(&toast.title),
        xml_escape(&toast.message)
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{ImageRef, ImageShape};
    use crate::toast::ToastRequest;

    fn toast(image: Option<ImageRef>) -> ToastRequest {
        ToastRequest {
            title: "Tony Redmond".into(),
            message: "is now available".into(),
            attribution: Some("Microsoft Teams".into()),
            action_label: Some("Open <chat>".into()),
            action_url: Some("https://teams.example/chat?a=1&b=2".into()),
            sources: Vec::new(),
            image,
        }
    }

    #[test]
    fn no_image_builds_todays_xml() {
        let xml = build_toast_xml(&toast(None), None);
        assert!(xml.starts_with("<toast><visual><binding template=\"ToastGeneric\">"));
        assert!(xml.contains("<text>Tony Redmond</text>"));
        assert!(!xml.contains("<image"));
        // escaping in action attributes
        assert!(xml.contains("Open &lt;chat&gt;"));
        assert!(xml.contains("a=1&amp;b=2"));
    }

    #[test]
    fn circle_image_gets_applogo_with_crop() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Circle };
        let xml = build_toast_xml(&toast(Some(image)), Some(std::path::Path::new("/tmp/cache/abc123")));
        assert!(xml.contains(r#"<image placement="appLogoOverride" hint-crop="circle" src="file:///tmp/cache/abc123"/>"#));
    }

    #[test]
    fn square_image_omits_crop_attribute() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Square };
        let xml = build_toast_xml(&toast(Some(image)), Some(std::path::Path::new("/tmp/cache/abc123")));
        assert!(xml.contains(r#"<image placement="appLogoOverride" src="file:///tmp/cache/abc123"/>"#));
        assert!(!xml.contains("hint-crop"));
    }

    #[test]
    fn image_ref_without_local_path_renders_imageless() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Circle };
        let xml = build_toast_xml(&toast(Some(image)), None); // fetch failed
        assert!(!xml.contains("<image"));
    }

    #[test]
    fn windows_backslash_paths_become_forward_slash_file_uris() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Square };
        let xml = build_toast_xml(
            &toast(Some(image)),
            Some(std::path::Path::new(r"C:\Users\u\AppData\Local\DesktopNotificationAgent\image-cache\abc")),
        );
        assert!(xml.contains(r#"src="file:///C:/Users/u/AppData/Local/DesktopNotificationAgent/image-cache/abc"/>"#));
    }

    #[test]
    fn paths_with_spaces_are_percent_encoded() {
        let image = ImageRef { url: "https://x/a.jpg".into(), shape: ImageShape::Square };
        let xml = build_toast_xml(
            &toast(Some(image)),
            Some(std::path::Path::new(r"C:\Users\John Smith\AppData\Local\DesktopNotificationAgent\image-cache\abc")),
        );
        assert!(xml.contains(r#"src="file:///C:/Users/John%20Smith/AppData/Local/DesktopNotificationAgent/image-cache/abc"/>"#));
    }
}
