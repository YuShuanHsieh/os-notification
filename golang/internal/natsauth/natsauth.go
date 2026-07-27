// Package natsauth resolves NATS authentication for the agent's connection
// (design: NATS WebSocket + pluggable auth). Mirrors the IdentityProvider
// pattern used elsewhere in this repository: a simple default (CredsFileAuth)
// lives here; richer composition (e.g. an external auth service reusing an
// AAD identity, as ported in rust/notify-agent-core's ExternalAuthServiceAuth)
// is out of scope for this package and, if needed, belongs in a host's main.
package natsauth

import (
	"context"
	"fmt"
	"os"

	"github.com/nats-io/nats.go"
)

// Provider supplies nats.Option(s) that configure authentication for a
// connection attempt. Returning an error means the auth configuration itself
// is invalid (e.g. the creds file doesn't exist) — the caller should fail
// startup rather than attempt an unauthenticated connection.
type Provider interface {
	Options(ctx context.Context) ([]nats.Option, error)
}

// CredsFileAuth authenticates using a standard NATS .creds file (JWT + NKey
// seed) at Path.
type CredsFileAuth struct {
	Path string
}

// Options validates that Path exists and, if so, returns the nats.Option that
// configures the connection to authenticate from that creds file.
func (c CredsFileAuth) Options(ctx context.Context) ([]nats.Option, error) {
	if _, err := os.Stat(c.Path); err != nil {
		return nil, fmt.Errorf("nats auth [creds-file]: %w", err)
	}
	return []nats.Option{nats.UserCredentials(c.Path)}, nil
}
