package ovh

import (
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestProviderName(t *testing.T) {
	p := Provider{}
	if p.Name() != "ovh" {
		t.Errorf("expected ovh, got %s", p.Name())
	}
}

func TestProviderAliases(t *testing.T) {
	p := Provider{}
	aliases := p.Aliases()
	found := map[string]bool{}
	for _, a := range aliases {
		found[a] = true
	}
	if !found["ovhcloud"] {
		t.Error("expected ovhcloud alias")
	}
	if !found["ovh-cloud"] {
		t.Error("expected ovh-cloud alias")
	}
}

func TestProviderSpec(t *testing.T) {
	p := Provider{}
	spec := p.Spec()
	if spec.Name != "ovh" {
		t.Errorf("expected name ovh, got %s", spec.Name)
	}
	if spec.Kind != core.ProviderKindSSHLease {
		t.Errorf("expected SSHLease kind, got %s", spec.Kind)
	}
	if len(spec.Targets) != 1 || spec.Targets[0].OS != core.TargetLinux {
		t.Error("expected linux-only target")
	}
}

func TestProviderConfigure(t *testing.T) {
	p := Provider{}
	cfg := core.Config{Provider: "ovh"}
	rt := core.Runtime{}
	backend, err := p.Configure(cfg, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend == nil {
		t.Fatal("expected non-nil backend")
	}
	if backend.Spec().Name != "ovh" {
		t.Errorf("expected spec name ovh, got %s", backend.Spec().Name)
	}
}
