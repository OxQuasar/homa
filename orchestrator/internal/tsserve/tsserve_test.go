package tsserve_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/sandbox/runnertest"
	"github.com/skipper/homa/orchestrator/internal/tsserve"
)

// TestRegisterEmitsExpectedArgv — the captain's mandated Register-argv check.
func TestRegisterEmitsExpectedArgv(t *testing.T) {
	fr := &runnertest.FakeRunner{}
	svc := tsserve.New("tailscale", fr)
	if err := svc.Register(context.Background(), 10001, "http://localhost:40001"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	calls := fr.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "tailscale" {
		t.Errorf("bin: got %q, want tailscale", calls[0].Name)
	}
	wantArgs := []string{"serve", "--bg", "--https=10001", "http://localhost:40001"}
	if !reflect.DeepEqual(calls[0].Args, wantArgs) {
		t.Errorf("args:\n got  %v\n want %v", calls[0].Args, wantArgs)
	}
}

func TestUnregisterEmitsExpectedArgv(t *testing.T) {
	fr := &runnertest.FakeRunner{}
	svc := tsserve.New("tailscale", fr)
	if err := svc.Unregister(context.Background(), 10001); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	calls := fr.Calls()
	wantArgs := []string{"serve", "--https=10001", "off"}
	if !reflect.DeepEqual(calls[0].Args, wantArgs) {
		t.Errorf("args:\n got  %v\n want %v", calls[0].Args, wantArgs)
	}
}

// TestUnregisterAbsentMappingTolerated — exit-error from tailscale (no such
// mapping) must surface as success, matching Unregister's idempotent
// contract.
func TestUnregisterAbsentMappingTolerated(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, _ []string) ([]byte, error) {
			return nil, runnertest.ExitError(name, 1, "no serve config to remove")
		},
	}
	svc := tsserve.New("tailscale", fr)
	if err := svc.Unregister(context.Background(), 10001); err != nil {
		t.Errorf("Unregister: %v", err)
	}
}

// TestRegisterIdempotent — calling Register twice should succeed both times
// (we rely on tailscale serve being a write-or-update operation).
func TestRegisterIdempotent(t *testing.T) {
	fr := &runnertest.FakeRunner{}
	svc := tsserve.New("tailscale", fr)
	if err := svc.Register(context.Background(), 10001, "http://localhost:40001"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := svc.Register(context.Background(), 10001, "http://localhost:40001"); err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if len(fr.Calls()) != 2 {
		t.Errorf("expected 2 calls, got %d", len(fr.Calls()))
	}
}

// Tailscale serve status keys its Web map by "<host>:<servePort>"; that's
// what we substring-match against the port suffix.
func TestIsRegisteredFindsPort(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(_ string, _ []string) ([]byte, error) {
			return []byte(`{"Web": {"machine.tailnet.ts.net:10001": {"Handlers": {}}, "machine.tailnet.ts.net:10002": {}}}`), nil
		},
	}
	svc := tsserve.New("tailscale", fr)
	got, err := svc.IsRegistered(context.Background(), 10001)
	if err != nil {
		t.Fatalf("IsRegistered: %v", err)
	}
	if !got {
		t.Error("got false, want true")
	}
}

func TestIsRegisteredMissingPort(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(_ string, _ []string) ([]byte, error) {
			return []byte(`{"Web": {"machine.tailnet.ts.net:10002": {}}}`), nil
		},
	}
	svc := tsserve.New("tailscale", fr)
	got, err := svc.IsRegistered(context.Background(), 10001)
	if err != nil {
		t.Fatalf("IsRegistered: %v", err)
	}
	if got {
		t.Error("got true, want false")
	}
}
