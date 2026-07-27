package main

import (
	"fmt"
	"strconv"
	"strings"
)

// nextValue advances *i past the flag token at *i and returns the value
// token that follows, mirroring the Rust reference's next_value: it always
// consumes one extra token so the outer loop's subsequent i++ lands on the
// token after the value.
func nextValue(flags []string, i *int, flag string) (string, error) {
	*i++
	if *i >= len(flags) {
		return "", fmt.Errorf("%s needs a value", flag)
	}
	return flags[*i], nil
}

// parseCount parses a legacy/flag count value the same way the C# and Rust
// references do: as a 32-bit signed integer (so out-of-i32-range input is
// rejected, not silently truncated), required to be >= 1.
func parseCount(s string) (int, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil || v < 1 {
		return 0, fmt.Errorf("not a positive integer")
	}
	return int(v), nil
}

// parseArgs parses argv (excluding argv[0], the program name) into a
// PublishSpec, following the precedence defaults -> scenario preset ->
// legacy positionals -> named flags (flags always win). It is a line-for-
// line port of rust/test-publisher/src/args.rs's parse function; the error
// message strings below are the ones printed to the user and must match
// the Rust/C# references verbatim.
func parseArgs(args []string) (PublishSpec, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return PublishSpec{}, fmt.Errorf("first argument must be <userId>")
	}

	spec := Defaults(args[0])
	rest := args[1:]

	splitAt := len(rest)
	for idx, a := range rest {
		if strings.HasPrefix(a, "--") {
			splitAt = idx
			break
		}
	}
	positionals := rest[:splitAt]
	flags := rest[splitAt:]

	// Naive scan for --scenario across every flag token (not skipping other
	// flags' values), matching the Rust reference exactly. The last
	// occurrence wins.
	var scenario *string
	for i := 0; i < len(flags); i++ {
		if flags[i] == "--scenario" {
			if i+1 >= len(flags) {
				return PublishSpec{}, fmt.Errorf("--scenario needs a value")
			}
			scenario = &flags[i+1]
		}
	}

	if scenario != nil && len(positionals) > 0 {
		return PublishSpec{}, fmt.Errorf("--scenario cannot be combined with legacy positional arguments")
	}

	if scenario != nil {
		if !ApplyScenario(&spec, *scenario) {
			return PublishSpec{}, fmt.Errorf("unknown scenario '%s' (presence|invoice|progress|batch|dedup)", *scenario)
		}
	}

	if len(positionals) > 5 {
		return PublishSpec{}, fmt.Errorf("too many legacy positional arguments")
	}
	if len(positionals) > 0 {
		spec.Title = positionals[0]
	}
	if len(positionals) > 1 {
		spec.Message = positionals[1]
	}
	if len(positionals) > 2 {
		spec.Priority = positionals[2]
	}
	if len(positionals) > 3 {
		c, err := parseCount(positionals[3])
		if err != nil {
			return PublishSpec{}, fmt.Errorf("count must be a positive integer")
		}
		spec.Count = c
	}
	if len(positionals) > 4 {
		v := positionals[4]
		spec.ImageURL = &v
	}

	for i := 0; i < len(flags); i++ {
		flag := flags[i]
		switch flag {
		case "--scenario":
			i++ // already applied above
		case "--title":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.Title = v
		case "--message":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.Message = v
			spec.Messages = nil
		case "--secondary":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.Secondary = &v
		case "--type":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.NotificationType = v
		case "--priority":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.Priority = v
		case "--count":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			c, err := parseCount(v)
			if err != nil {
				return PublishSpec{}, fmt.Errorf("--count must be a positive integer")
			}
			spec.Count = c
			spec.Messages = nil
		case "--image-url":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.ImageURL = &v
		case "--image-shape":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			if v != "circle" && v != "square" {
				return PublishSpec{}, fmt.Errorf("--image-shape must be circle or square")
			}
			spec.ImageShape = v
		case "--action-label":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.ActionLabel = &v
		case "--action-url":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.ActionURL = &v
		case "--agg-key":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.AggKey = &v
		case "--dedup-key":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			spec.DedupKey = &v
		case "--replaceable":
			spec.Replaceable = true
		case "--delay-ms":
			v, err := nextValue(flags, &i, flag)
			if err != nil {
				return PublishSpec{}, err
			}
			d, err := strconv.ParseInt(v, 10, 32)
			if err != nil || d < 0 {
				return PublishSpec{}, fmt.Errorf("--delay-ms must be a non-negative integer")
			}
			spec.DelayMs = int(d)
		default:
			return PublishSpec{}, fmt.Errorf("unknown flag '%s'", flag)
		}
	}

	return spec, nil
}
