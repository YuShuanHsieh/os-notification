#[derive(Debug, Clone, PartialEq)]
pub struct PublishSpec {
    pub user_id: String,
    pub title: String,
    pub message: String,
    pub secondary: Option<String>,
    pub notification_type: String,
    pub priority: String,
    pub count: u32,
    pub image_url: Option<String>,
    pub image_shape: String,
    pub action_label: Option<String>,
    pub action_url: Option<String>,
    pub agg_key: Option<String>,
    pub dedup_key: Option<String>,
    pub replaceable: bool,
    pub delay_ms: u64,
    pub messages: Option<Vec<String>>,
    pub expect: Option<String>,
}

impl PublishSpec {
    pub fn defaults(user_id: impl Into<String>) -> Self {
        Self {
            user_id: user_id.into(),
            title: "Invoice ready".to_string(),
            message: "Invoice INV-8492 is ready for review.".to_string(),
            secondary: Some("TestPublisher".to_string()),
            notification_type: "billing.invoice.ready".to_string(),
            priority: "normal".to_string(),
            count: 1,
            image_url: None,
            image_shape: "circle".to_string(),
            action_label: Some("View".to_string()),
            action_url: Some("https://app.example.com/invoices/8492".to_string()),
            agg_key: None,
            dedup_key: None,
            replaceable: false,
            delay_ms: 0,
            messages: None,
            expect: None,
        }
    }

    pub fn resolve_messages(&self) -> Vec<String> {
        self.messages
            .clone()
            .unwrap_or_else(|| std::iter::repeat_n(self.message.clone(), self.count as usize).collect())
    }
}

pub fn apply_scenario(spec: &mut PublishSpec, name: &str) -> bool {
    match name {
        "presence" => {
            spec.title = "Tony Redmond".to_string();
            spec.message = "is now available".to_string();
            spec.secondary = Some("Microsoft Teams".to_string());
            spec.notification_type = "presence.available".to_string();
            spec.priority = "critical".to_string();
            spec.image_url = Some("https://i.pravatar.cc/96?u=tony".to_string());
            spec.image_shape = "circle".to_string();
            spec.action_label = Some("Open chat".to_string());
            spec.action_url = Some("https://teams.example.com/chat/tony".to_string());
            spec.expect = Some("1 avatar toast, 2 acks".to_string());
            true
        }
        "invoice" => {
            spec.title = "Invoice ready".to_string();
            spec.message = "Invoice INV-8492 is ready for review.".to_string();
            spec.secondary = Some("Contoso Billing".to_string());
            spec.notification_type = "billing.invoice.ready".to_string();
            spec.priority = "normal".to_string();
            spec.action_label = Some("View invoice".to_string());
            spec.action_url = Some("https://app.example.com/invoices/8492".to_string());
            spec.expect = Some("1 toast after ~10s, 2 acks".to_string());
            true
        }
        "progress" => {
            spec.title = "Export job".to_string();
            spec.notification_type = "job.progress".to_string();
            spec.agg_key = Some("job.progress".to_string());
            spec.priority = "normal".to_string();
            spec.replaceable = true;
            spec.delay_ms = 100;
            spec.messages = Some(vec!["10%".to_string(), "60%".to_string(), "90%".to_string()]);
            spec.expect = Some("after ~10s ONE toast showing 90%".to_string());
            true
        }
        "batch" => {
            spec.title = "Batch demo".to_string();
            spec.agg_key = Some("demo.batch".to_string());
            spec.priority = "normal".to_string();
            spec.delay_ms = 100;
            spec.messages = Some(vec!["first".to_string(), "second".to_string(), "third".to_string()]);
            spec.expect = Some("ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt".to_string());
            true
        }
        "dedup" => {
            spec.priority = "critical".to_string();
            spec.dedup_key = Some("dedup-demo".to_string());
            spec.count = 3;
            spec.expect = Some("ONE toast, exactly 2 acks (duplicates dropped)".to_string());
            true
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults_match_the_legacy_baseline() {
        let spec = PublishSpec::defaults("u1");
        assert_eq!(spec.user_id, "u1");
        assert_eq!(spec.title, "Invoice ready");
        assert_eq!(spec.message, "Invoice INV-8492 is ready for review.");
        assert_eq!(spec.secondary.as_deref(), Some("TestPublisher"));
        assert_eq!(spec.notification_type, "billing.invoice.ready");
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.count, 1);
        assert_eq!(spec.image_url, None);
        assert_eq!(spec.image_shape, "circle");
        assert_eq!(spec.action_label.as_deref(), Some("View"));
        assert_eq!(spec.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(spec.agg_key, None);
        assert_eq!(spec.dedup_key, None);
        assert!(!spec.replaceable);
        assert_eq!(spec.delay_ms, 0);
        assert_eq!(spec.messages, None);
        assert_eq!(spec.expect, None);
    }

    #[test]
    fn unknown_scenario_returns_false_and_leaves_spec_untouched() {
        let mut spec = PublishSpec::defaults("u1");
        let before = spec.clone();
        assert!(!apply_scenario(&mut spec, "not-a-scenario"));
        assert_eq!(spec, before);
    }

    #[test]
    fn presence_scenario() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "presence"));
        assert_eq!(spec.title, "Tony Redmond");
        assert_eq!(spec.message, "is now available");
        assert_eq!(spec.secondary.as_deref(), Some("Microsoft Teams"));
        assert_eq!(spec.notification_type, "presence.available");
        assert_eq!(spec.priority, "critical");
        assert_eq!(spec.image_url.as_deref(), Some("https://i.pravatar.cc/96?u=tony"));
        assert_eq!(spec.image_shape, "circle");
        assert_eq!(spec.action_label.as_deref(), Some("Open chat"));
        assert_eq!(spec.action_url.as_deref(), Some("https://teams.example.com/chat/tony"));
        assert_eq!(spec.expect.as_deref(), Some("1 avatar toast, 2 acks"));
    }

    #[test]
    fn invoice_scenario() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "invoice"));
        assert_eq!(spec.title, "Invoice ready");
        assert_eq!(spec.message, "Invoice INV-8492 is ready for review.");
        assert_eq!(spec.secondary.as_deref(), Some("Contoso Billing"));
        assert_eq!(spec.notification_type, "billing.invoice.ready");
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.action_label.as_deref(), Some("View invoice"));
        assert_eq!(spec.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(spec.expect.as_deref(), Some("1 toast after ~10s, 2 acks"));
    }

    #[test]
    fn progress_scenario_leaves_untouched_fields_at_their_defaults() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "progress"));
        assert_eq!(spec.title, "Export job");
        assert_eq!(spec.notification_type, "job.progress");
        assert_eq!(spec.agg_key.as_deref(), Some("job.progress"));
        assert_eq!(spec.priority, "normal");
        assert!(spec.replaceable);
        assert_eq!(spec.delay_ms, 100);
        assert_eq!(spec.messages, Some(vec!["10%".to_string(), "60%".to_string(), "90%".to_string()]));
        assert_eq!(spec.expect.as_deref(), Some("after ~10s ONE toast showing 90%"));
        // progress doesn't touch these — they stay at PublishSpec::defaults() values.
        assert_eq!(spec.action_label.as_deref(), Some("View"));
        assert_eq!(spec.action_url.as_deref(), Some("https://app.example.com/invoices/8492"));
        assert_eq!(spec.secondary.as_deref(), Some("TestPublisher"));
    }

    #[test]
    fn batch_scenario_leaves_untouched_fields_at_their_defaults() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "batch"));
        assert_eq!(spec.title, "Batch demo");
        assert_eq!(spec.agg_key.as_deref(), Some("demo.batch"));
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.delay_ms, 100);
        assert_eq!(spec.messages, Some(vec!["first".to_string(), "second".to_string(), "third".to_string()]));
        assert_eq!(spec.expect.as_deref(), Some("ONE '3 notifications — demo.batch' toast, 6 acks sharing one toastSubmittedAt"));
        // batch doesn't touch these — they stay at PublishSpec::defaults() values.
        assert_eq!(spec.notification_type, "billing.invoice.ready");
        assert_eq!(spec.action_label.as_deref(), Some("View"));
    }

    #[test]
    fn dedup_scenario_leaves_untouched_fields_at_their_defaults() {
        let mut spec = PublishSpec::defaults("u1");
        assert!(apply_scenario(&mut spec, "dedup"));
        assert_eq!(spec.priority, "critical");
        assert_eq!(spec.dedup_key.as_deref(), Some("dedup-demo"));
        assert_eq!(spec.count, 3);
        assert_eq!(spec.expect.as_deref(), Some("ONE toast, exactly 2 acks (duplicates dropped)"));
        // dedup doesn't touch these — they stay at PublishSpec::defaults() values.
        assert_eq!(spec.title, "Invoice ready");
        assert_eq!(spec.notification_type, "billing.invoice.ready");
    }

    #[test]
    fn resolve_messages_uses_messages_list_when_set() {
        let mut spec = PublishSpec::defaults("u1");
        spec.messages = Some(vec!["a".to_string(), "b".to_string()]);
        spec.count = 5; // should be ignored — messages list wins
        assert_eq!(spec.resolve_messages(), vec!["a".to_string(), "b".to_string()]);
    }

    #[test]
    fn resolve_messages_replicates_message_by_count_when_no_messages_list() {
        let mut spec = PublishSpec::defaults("u1");
        spec.message = "hi".to_string();
        spec.count = 3;
        assert_eq!(spec.resolve_messages(), vec!["hi".to_string(), "hi".to_string(), "hi".to_string()]);
    }
}
