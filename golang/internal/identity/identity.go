// Package identity resolves the immutable application user ID and device ID
// (design §8). The OS account name is never used as identity.
package identity

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Identity is the resolved application identity for this agent instance.
type Identity struct {
	UserID   string
	DeviceID string
}

// Provider resolves the application identity. Implementations may block
// (e.g. an interactive sign-in flow) or return quickly (environment vars).
type Provider interface {
	Resolve(ctx context.Context) (Identity, error)
}

// EnvIdentity resolves identity from NOTIFY_USER_ID (required) and
// NOTIFY_DEVICE_ID (defaults to "d-{lowercase hostname}" when unset/blank).
// This is development identity, matching the C# EnvironmentIdentityProvider
// and Rust EnvIdentity.
type EnvIdentity struct{}

// Resolve implements Provider.
func (EnvIdentity) Resolve(ctx context.Context) (Identity, error) {
	userID := strings.TrimSpace(os.Getenv("NOTIFY_USER_ID"))
	if userID == "" {
		return Identity{}, fmt.Errorf("NOTIFY_USER_ID is not set")
	}

	deviceID := strings.TrimSpace(os.Getenv("NOTIFY_DEVICE_ID"))
	if deviceID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return Identity{}, fmt.Errorf("resolve device id: %w", err)
		}
		deviceID = "d-" + strings.ToLower(hostname)
	}

	return Identity{UserID: userID, DeviceID: deviceID}, nil
}
