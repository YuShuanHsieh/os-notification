package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

// generateEventID returns an id of the form "evt-<32 lowercase hex chars>",
// matching the Rust reference's 16 random bytes hex-encoded.
func generateEventID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "evt-" + hex.EncodeToString(buf), nil
}

// printUsage writes the CLI usage block to stderr. The text matches
// rust/test-publisher/src/main.rs's print_usage verbatim (it already uses
// "test-publisher" as the program name).
func printUsage(errMsg string) {
	fmt.Fprintf(os.Stderr, "error: %s\n", errMsg)
	fmt.Fprintln(os.Stderr, "usage: test-publisher <userId> [title] [message] [priority] [count] [imageUrl]")
	fmt.Fprintln(os.Stderr, "       test-publisher <userId> --scenario presence|invoice|progress|batch|dedup [--flags]")
	fmt.Fprintln(os.Stderr, "       test-publisher <userId> [--flags]")
	fmt.Fprintln(os.Stderr, "flags: --title --message --secondary --type --priority --count --image-url")
	fmt.Fprintln(os.Stderr, "       --image-shape circle|square --action-label --action-url --agg-key")
	fmt.Fprintln(os.Stderr, "       --dedup-key --replaceable --delay-ms")
}

func getEnvOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(cliArgs []string) int {
	spec, err := parseArgs(cliArgs)
	if err != nil {
		printUsage(err.Error())
		return 2
	}

	natsURL := getEnvOr("NOTIFY_NATS_URL", "nats://127.0.0.1:4222")
	ackSubject := getEnvOr("NOTIFY_ACK_SUBJECT", "notify.ack.desktop")
	subject := fmt.Sprintf("notify.user.%s.desktop", spec.UserID)

	if spec.Expect != nil {
		fmt.Printf("EXPECT: %s\n", *spec.Expect)
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer nc.Close()

	// Subscribe to the ack subject BEFORE publishing, then let the
	// subscription settle, matching the C#/Rust references.
	sub, err := nc.Subscribe(ackSubject, func(msg *nats.Msg) {
		fmt.Printf("[ACK] %s\n", string(msg.Data))
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer func() { _ = sub.Unsubscribe() }()
	time.Sleep(300 * time.Millisecond)

	messages := spec.ResolveMessages()
	for i, message := range messages {
		eventID, err := generateEventID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		payload := buildPayload(&spec, message, eventID)
		body, err := json.Marshal(payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := nc.Publish(subject, body); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("[PUB] %s -> %s (priority=%s)\n", eventID, subject, spec.Priority)
		if spec.DelayMs > 0 && i+1 < len(messages) {
			time.Sleep(time.Duration(spec.DelayMs) * time.Millisecond)
		}
	}
	_ = nc.Flush()

	// Outlast the 10s normal-priority aggregation window before unsubscribing.
	time.Sleep(12 * time.Second)

	return 0
}
