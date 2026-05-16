package ovh

import (
	"context"
	"io"
	"os"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
	"github.com/openclaw/crabbox/internal/providers/shared"
)

// Type aliases for the core package types used throughout.
type Config = core.Config
type Runtime = core.Runtime
type ProviderSpec = core.ProviderSpec
type Backend = core.Backend
type AcquireRequest = core.AcquireRequest
type ResolveRequest = core.ResolveRequest
type ListRequest = core.ListRequest
type LeaseView = core.LeaseView
type ReleaseLeaseRequest = core.ReleaseLeaseRequest
type TouchRequest = core.TouchRequest
type CleanupRequest = core.CleanupRequest
type LeaseTarget = core.LeaseTarget
type Server = core.Server
type SSHTarget = core.SSHTarget

const ovhSSHUser = "ubuntu" // default for Ubuntu images

// Helper stubs (matching GCP provider pattern)
func newOVHClient(cfg Config) (*core.OVHClient, error) {
	return core.NewOVHClient(cfg)
}

func exit(code int, format string, args ...any) core.ExitError {
	return core.Exit(code, format, args...)
}

func newLeaseID() string { return core.NewLeaseID() }
func allocateDirectLeaseSlug(id string, servers []Server) string {
	return core.AllocateDirectLeaseSlug(id, servers)
}
func ensureTestboxKeyForConfig(cfg Config, leaseID string) (string, string, error) {
	return core.EnsureTestboxKeyForConfig(cfg, leaseID)
}
func providerKeyForLease(leaseID string) string { return core.ProviderKeyForLease(leaseID) }
func sshTargetFromConfig(cfg Config, host string) SSHTarget {
	return core.SSHTargetFromConfig(cfg, host)
}
func waitForSSHReady(ctx context.Context, target *SSHTarget, stderr io.Writer, phase string, timeout time.Duration) error {
	return core.WaitForSSHReady(ctx, target, stderr, phase, timeout)
}
func bootstrapWaitTimeout(cfg Config) time.Duration { return core.BootstrapWaitTimeout(cfg) }
func blank(value, fallback string) string             { return core.Blank(value, fallback) }
func useStoredTestboxKey(target *SSHTarget, leaseID string) {
	if keyPath, err := core.TestboxKeyPath(leaseID); err == nil {
		if _, statErr := os.Stat(keyPath); statErr == nil {
			target.Key = keyPath
		}
	}
}
func removeLeaseClaim(leaseID string)   { core.RemoveLeaseClaim(leaseID) }
func leaseProviderName(leaseID, slug string) string {
	return core.LeaseProviderName(leaseID, slug)
}
func acquireAttemptsRetry(rt Runtime, keep bool, acquire func() (LeaseTarget, error)) (LeaseTarget, error) {
	return shared.AcquireAttemptsRetry(rt, keep, acquire)
}
