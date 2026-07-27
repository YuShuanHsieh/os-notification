package natsauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCredsFileAuthFailsOnMissingFile(t *testing.T) {
	provider := CredsFileAuth{Path: "/does/not/exist"}

	opts, err := provider.Options(context.Background())

	if err == nil {
		t.Fatalf("Options() error = nil, want non-nil for missing creds file")
	}
	if opts != nil {
		t.Fatalf("Options() opts = %v, want nil when error is returned", opts)
	}
}

func TestCredsFileAuthSucceedsOnExistingFile(t *testing.T) {
	// Contents don't need to be a fully valid JWT/NKey pair for this thin wrapper's
	// contract: Options only needs to confirm the file exists and hand back the
	// nats.UserCredentials option. Actual JWT/NKey parsing happens inside nats.go
	// at connect time, not here.
	path := filepath.Join(t.TempDir(), "user.creds")
	contents := "-----BEGIN NATS USER JWT-----\nfake.jwt.value\n------END NATS USER JWT------\n\n" +
		"-----BEGIN USER NKEY SEED-----\nSUFAKESEEDVALUE\n------END USER NKEY SEED------\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write temp creds file: %v", err)
	}

	provider := CredsFileAuth{Path: path}

	opts, err := provider.Options(context.Background())

	if err != nil {
		t.Fatalf("Options() error = %v, want nil for existing creds file", err)
	}
	if len(opts) != 1 {
		t.Fatalf("Options() len = %d, want 1", len(opts))
	}
}
