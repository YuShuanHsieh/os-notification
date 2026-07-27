package identity

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestEnvIdentityResolveUserAndDeviceIDSet(t *testing.T) {
	t.Setenv("NOTIFY_USER_ID", "u_demo")
	t.Setenv("NOTIFY_DEVICE_ID", "d-test")

	got, err := (EnvIdentity{}).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	want := Identity{UserID: "u_demo", DeviceID: "d-test"}
	if got != want {
		t.Fatalf("Resolve() = %+v, want %+v", got, want)
	}
}

func TestEnvIdentityResolveDeviceIDDefaultsToHostname(t *testing.T) {
	t.Setenv("NOTIFY_USER_ID", "u_demo")
	t.Setenv("NOTIFY_DEVICE_ID", "")

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname() error = %v", err)
	}
	wantDeviceID := "d-" + strings.ToLower(hostname)

	got, err := (EnvIdentity{}).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	if got.UserID != "u_demo" {
		t.Fatalf("Resolve().UserID = %q, want %q", got.UserID, "u_demo")
	}
	if got.DeviceID != wantDeviceID {
		t.Fatalf("Resolve().DeviceID = %q, want %q", got.DeviceID, wantDeviceID)
	}
}

func TestEnvIdentityResolveDeviceIDUnsetDefaultsToHostname(t *testing.T) {
	t.Setenv("NOTIFY_USER_ID", "u_demo")
	// t.Setenv to "" plus Unsetenv ensures NOTIFY_DEVICE_ID is truly absent (not
	// just empty) from the process environment, while still getting t.Setenv's
	// automatic restore-on-cleanup behavior.
	t.Setenv("NOTIFY_DEVICE_ID", "")
	os.Unsetenv("NOTIFY_DEVICE_ID")

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname() error = %v", err)
	}
	wantDeviceID := "d-" + strings.ToLower(hostname)

	got, err := (EnvIdentity{}).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}

	if got.DeviceID != wantDeviceID {
		t.Fatalf("Resolve().DeviceID = %q, want %q", got.DeviceID, wantDeviceID)
	}
}

func TestEnvIdentityResolveErrorsWhenUserIDUnset(t *testing.T) {
	// t.Setenv to "" plus Unsetenv ensures the var is truly absent (not just
	// empty) from the process environment for the duration of the test, while
	// still getting t.Setenv's automatic restore-on-cleanup behavior.
	t.Setenv("NOTIFY_USER_ID", "")
	os.Unsetenv("NOTIFY_USER_ID")
	t.Setenv("NOTIFY_DEVICE_ID", "d-test")

	got, err := (EnvIdentity{}).Resolve(context.Background())
	if err == nil {
		t.Fatalf("Resolve() error = nil, want non-nil")
	}
	if got != (Identity{}) {
		t.Fatalf("Resolve() = %+v, want zero-value Identity on error", got)
	}
}

func TestEnvIdentityResolveErrorsWhenUserIDBlank(t *testing.T) {
	t.Setenv("NOTIFY_USER_ID", "   ")
	t.Setenv("NOTIFY_DEVICE_ID", "d-test")

	got, err := (EnvIdentity{}).Resolve(context.Background())
	if err == nil {
		t.Fatalf("Resolve() error = nil, want non-nil")
	}
	if got != (Identity{}) {
		t.Fatalf("Resolve() = %+v, want zero-value Identity on error", got)
	}
}
