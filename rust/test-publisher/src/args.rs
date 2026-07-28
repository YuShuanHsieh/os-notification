use crate::spec::{apply_scenario, PublishSpec};

fn next_value(flags: &[String], i: &mut usize, flag: &str) -> Result<String, String> {
    *i += 1;
    flags.get(*i).cloned().ok_or_else(|| format!("{flag} needs a value"))
}

pub fn parse(args: &[String]) -> Result<PublishSpec, String> {
    if args.is_empty() || args[0].starts_with("--") {
        return Err("first argument must be <userId>".to_string());
    }

    let mut spec = PublishSpec::defaults(args[0].clone());
    let rest = &args[1..];
    let split_at = rest.iter().position(|a| a.starts_with("--")).unwrap_or(rest.len());
    let positionals = &rest[..split_at];
    let flags = &rest[split_at..];

    let mut scenario: Option<&str> = None;
    for i in 0..flags.len() {
        if flags[i] == "--scenario" {
            scenario = Some(flags.get(i + 1).ok_or_else(|| "--scenario needs a value".to_string())?.as_str());
        }
    }

    if scenario.is_some() && !positionals.is_empty() {
        return Err("--scenario cannot be combined with legacy positional arguments".to_string());
    }

    if let Some(name) = scenario {
        if !apply_scenario(&mut spec, name) {
            return Err(format!("unknown scenario '{name}' (presence|invoice|progress|batch|dedup)"));
        }
    }

    if positionals.len() > 5 {
        return Err("too many legacy positional arguments".to_string());
    }
    if let Some(v) = positionals.first() {
        spec.title = v.clone();
    }
    if let Some(v) = positionals.get(1) {
        spec.message = v.clone();
    }
    if let Some(v) = positionals.get(2) {
        spec.priority = v.clone();
    }
    if let Some(v) = positionals.get(3) {
        let c: i32 = v.parse().map_err(|_| "count must be a positive integer".to_string())?;
        if c < 1 {
            return Err("count must be a positive integer".to_string());
        }
        spec.count = c as u32;
    }
    if let Some(v) = positionals.get(4) {
        spec.image_url = Some(v.clone());
    }

    let mut i = 0;
    while i < flags.len() {
        let flag = flags[i].clone();
        match flag.as_str() {
            "--scenario" => i += 1, // already applied above
            "--title" => spec.title = next_value(flags, &mut i, &flag)?,
            "--message" => {
                spec.message = next_value(flags, &mut i, &flag)?;
                spec.messages = None;
            }
            "--secondary" => spec.secondary = Some(next_value(flags, &mut i, &flag)?),
            "--type" => spec.notification_type = next_value(flags, &mut i, &flag)?,
            "--priority" => spec.priority = next_value(flags, &mut i, &flag)?,
            "--count" => {
                let v = next_value(flags, &mut i, &flag)?;
                let c: i32 = v.parse().map_err(|_| "--count must be a positive integer".to_string())?;
                if c < 1 {
                    return Err("--count must be a positive integer".to_string());
                }
                spec.count = c as u32;
                spec.messages = None;
            }
            "--image-url" => spec.image_url = Some(next_value(flags, &mut i, &flag)?),
            "--image-shape" => {
                let shape = next_value(flags, &mut i, &flag)?;
                if shape != "circle" && shape != "square" {
                    return Err("--image-shape must be circle or square".to_string());
                }
                spec.image_shape = shape;
            }
            "--action-label" => spec.action_label = Some(next_value(flags, &mut i, &flag)?),
            "--action-url" => spec.action_url = Some(next_value(flags, &mut i, &flag)?),
            "--agg-key" => spec.agg_key = Some(next_value(flags, &mut i, &flag)?),
            "--dedup-key" => spec.dedup_key = Some(next_value(flags, &mut i, &flag)?),
            "--replaceable" => spec.replaceable = true,
            "--delay-ms" => {
                let v = next_value(flags, &mut i, &flag)?;
                let d: i32 = v.parse().map_err(|_| "--delay-ms must be a non-negative integer".to_string())?;
                if d < 0 {
                    return Err("--delay-ms must be a non-negative integer".to_string());
                }
                spec.delay_ms = d as u64;
            }
            other => return Err(format!("unknown flag '{other}'")),
        }
        i += 1;
    }

    Ok(spec)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args(items: &[&str]) -> Vec<String> {
        items.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn empty_args_is_an_error() {
        let err = parse(&args(&[])).unwrap_err();
        assert_eq!(err, "first argument must be <userId>");
    }

    #[test]
    fn flag_as_first_arg_is_an_error() {
        let err = parse(&args(&["--title", "x"])).unwrap_err();
        assert_eq!(err, "first argument must be <userId>");
    }

    #[test]
    fn bare_user_id_uses_defaults() {
        let spec = parse(&args(&["u1"])).unwrap();
        assert_eq!(spec, PublishSpec::defaults("u1"));
    }

    #[test]
    fn legacy_positionals_fill_title_message_priority_count_image_url() {
        let spec = parse(&args(&["u1", "T", "M", "critical", "3", "https://img"])).unwrap();
        assert_eq!(spec.title, "T");
        assert_eq!(spec.message, "M");
        assert_eq!(spec.priority, "critical");
        assert_eq!(spec.count, 3);
        assert_eq!(spec.image_url.as_deref(), Some("https://img"));
    }

    #[test]
    fn too_many_legacy_positionals_is_an_error() {
        let err = parse(&args(&["u1", "T", "M", "critical", "3", "https://img", "extra"])).unwrap_err();
        assert_eq!(err, "too many legacy positional arguments");
    }

    #[test]
    fn invalid_legacy_count_is_an_error() {
        let err = parse(&args(&["u1", "T", "M", "critical", "zero"])).unwrap_err();
        assert_eq!(err, "count must be a positive integer");
        let err = parse(&args(&["u1", "T", "M", "critical", "0"])).unwrap_err();
        assert_eq!(err, "count must be a positive integer");
    }

    #[test]
    fn scenario_applies_preset_fields() {
        let spec = parse(&args(&["u1", "--scenario", "invoice"])).unwrap();
        let mut expected = PublishSpec::defaults("u1");
        apply_scenario(&mut expected, "invoice");
        assert_eq!(spec, expected);
    }

    #[test]
    fn unknown_scenario_is_an_error() {
        let err = parse(&args(&["u1", "--scenario", "nope"])).unwrap_err();
        assert_eq!(err, "unknown scenario 'nope' (presence|invoice|progress|batch|dedup)");
    }

    #[test]
    fn scenario_combined_with_legacy_positional_is_an_error() {
        let err = parse(&args(&["u1", "T", "--scenario", "invoice"])).unwrap_err();
        assert_eq!(err, "--scenario cannot be combined with legacy positional arguments");
    }

    #[test]
    fn flags_override_scenario_fields() {
        let spec = parse(&args(&["u1", "--scenario", "presence", "--priority", "normal"])).unwrap();
        assert_eq!(spec.priority, "normal");
        assert_eq!(spec.title, "Tony Redmond"); // untouched preset field survives
    }

    #[test]
    fn message_flag_clears_messages_list() {
        let spec = parse(&args(&["u1", "--scenario", "progress", "--message", "custom"])).unwrap();
        assert_eq!(spec.message, "custom");
        assert_eq!(spec.messages, None);
    }

    #[test]
    fn count_flag_clears_messages_list() {
        let spec = parse(&args(&["u1", "--scenario", "batch", "--count", "2"])).unwrap();
        assert_eq!(spec.count, 2);
        assert_eq!(spec.messages, None);
    }

    #[test]
    fn invalid_count_flag_is_an_error() {
        let err = parse(&args(&["u1", "--count", "0"])).unwrap_err();
        assert_eq!(err, "--count must be a positive integer");
    }

    #[test]
    fn image_shape_flag_validates_value() {
        let err = parse(&args(&["u1", "--image-shape", "hexagon"])).unwrap_err();
        assert_eq!(err, "--image-shape must be circle or square");
        let spec = parse(&args(&["u1", "--image-shape", "square"])).unwrap();
        assert_eq!(spec.image_shape, "square");
    }

    #[test]
    fn replaceable_flag_needs_no_value() {
        let spec = parse(&args(&["u1", "--replaceable"])).unwrap();
        assert!(spec.replaceable);
    }

    #[test]
    fn delay_ms_flag_validates_value() {
        let err = parse(&args(&["u1", "--delay-ms", "-1"])).unwrap_err();
        assert_eq!(err, "--delay-ms must be a non-negative integer");
        let spec = parse(&args(&["u1", "--delay-ms", "250"])).unwrap();
        assert_eq!(spec.delay_ms, 250);
    }

    #[test]
    fn unknown_flag_is_an_error() {
        let err = parse(&args(&["u1", "--nope"])).unwrap_err();
        assert_eq!(err, "unknown flag '--nope'");
    }

    #[test]
    fn flag_missing_value_is_an_error() {
        let err = parse(&args(&["u1", "--title"])).unwrap_err();
        assert_eq!(err, "--title needs a value");
    }

    #[test]
    fn count_beyond_i32_range_is_an_error() {
        let err = parse(&args(&["u1", "--count", "4294967297"])).unwrap_err();
        assert_eq!(err, "--count must be a positive integer");
    }
}
