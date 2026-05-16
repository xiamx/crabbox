package ovh

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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

// ovhLeaseBackend wraps shared.DirectSSHBackend for OVH Public Cloud.
type ovhLeaseBackend struct{ shared.DirectSSHBackend }

// NewOVHLeaseBackend creates a new OVH SSH lease backend.
func NewOVHLeaseBackend(spec ProviderSpec, cfg Config, rt Runtime) Backend {
	cfg.Provider = "ovh"
	return &ovhLeaseBackend{DirectSSHBackend: shared.DirectSSHBackend{SpecValue: spec, Cfg: cfg, RT: rt}}
}

// Acquire provisions a new OVH instance with retry.
func (b *ovhLeaseBackend) Acquire(ctx context.Context, req AcquireRequest) (LeaseTarget, error) {
	return acquireAttemptsRetry(b.RT, req.Keep, func() (LeaseTarget, error) {
		return b.acquireOnce(ctx, req.Keep)
	})
}

func (b *ovhLeaseBackend) acquireOnce(ctx context.Context, keep bool) (LeaseTarget, error) {
	client, err := newOVHClient(b.Cfg)
	if err != nil {
		return LeaseTarget{}, err
	}
	leaseID := newLeaseID()
	slug := allocateDirectLeaseSlug(leaseID, nil) // OVH: no pre-existing list needed for slug
	cfg := b.Cfg
	keyPath, publicKey, err := ensureTestboxKeyForConfig(cfg, leaseID)
	if err != nil {
		return LeaseTarget{}, err
	}
	cfg.SSHKey = keyPath
	cfg.ProviderKey = providerKeyForLease(leaseID)

	fmt.Fprintf(b.RT.Stderr, "provisioning provider=ovh lease=%s slug=%s region=%s class=%s keep=%v\n",
		leaseID, slug, cfg.OVH.Region, cfg.Class, keep)

	// Register SSH key
	sshKeyName := fmt.Sprintf("crabbox-%s", leaseID)
	sshKeyID, err := client.AddSSHKey(ctx, sshKeyName, publicKey, cfg.OVH.Region)
	if err != nil {
		return LeaseTarget{}, fmt.Errorf("ovh add ssh key: %w", err)
	}
	defer func() {
		if err != nil {
			_ = client.DeleteSSHKey(context.Background(), sshKeyID)
		}
	}()

	// Look up flavor
	flavors, err := client.ListFlavors(ctx, cfg.OVH.Region)
	if err != nil {
		return LeaseTarget{}, err
	}
	flavorID := findOVHFlavorID(flavors, cfg.ServerType)
	if flavorID == "" {
		return LeaseTarget{}, exit(3, "ovh flavor %q not found in region %s", cfg.ServerType, cfg.OVH.Region)
	}

	// Look up image
	images, err := client.ListImages(ctx, cfg.OVH.Region)
	if err != nil {
		return LeaseTarget{}, err
	}
	imageID := findOVHImageID(images, cfg.OVH.Image)
	if imageID == "" {
		return LeaseTarget{}, exit(3, "ovh image %q not found in region %s", cfg.OVH.Image, cfg.OVH.Region)
	}

	// Create instance
	instanceName := leaseProviderName(leaseID, slug)
	instanceID, err := client.CreateInstance(ctx, instanceName, flavorID, imageID, sshKeyID, cfg.OVH.Region)
	if err != nil {
		return LeaseTarget{}, err
	}
	// Cleanup on failure
	defer func() {
		if err != nil {
			_ = client.DeleteInstance(context.Background(), instanceID)
		}
	}()

	fmt.Fprintf(b.RT.Stderr, "provisioned lease=%s instance=%s region=%s flavor=%s image=%s\n",
		leaseID, instanceID, cfg.OVH.Region, cfg.ServerType, cfg.OVH.Image)

	// Wait for IP
	ip, err := client.WaitForInstanceIP(ctx, instanceID)
	if err != nil {
		return LeaseTarget{}, err
	}

	target := sshTargetFromConfig(cfg, ip)
	// OVH Ubuntu images use 'ubuntu' user
	if target.User == cfg.SSHUser {
		target.User = ovhSSHUser
	}
	if err := waitForSSHReady(ctx, &target, b.RT.Stderr, "bootstrap", bootstrapWaitTimeout(cfg)); err != nil {
		_ = client.DeleteInstance(context.Background(), instanceID)
		return LeaseTarget{}, err
	}

	var server Server
	server.CloudID = instanceID
	server.Provider = "ovh"
	server.Name = instanceName
	server.Status = "ACTIVE"
	server.Labels = map[string]string{
		"crabbox":  "true",
		"provider": "ovh",
		"lease":    leaseID,
		"region":   cfg.OVH.Region,
		"state":    "ready",
	}
	server.PublicNet.IPv4.IP = ip
	return LeaseTarget{Server: server, SSH: target, LeaseID: leaseID}, nil
}

// findOVHFlavorID looks up a flavor by name (case-insensitive).
func findOVHFlavorID(flavors []core.OVHFlavor, name string) string {
	for _, f := range flavors {
		if strings.EqualFold(f.Name, name) {
			return f.ID
		}
	}
	return ""
}

// findOVHImageID looks up an image by name substring (case-insensitive).
func findOVHImageID(images []core.OVHImage, name string) string {
	for _, img := range images {
		if strings.Contains(strings.ToLower(img.Name), strings.ToLower(name)) {
			return img.ID
		}
	}
	return ""
}

// Resolve finds an instance by ID or name.
func (b *ovhLeaseBackend) Resolve(ctx context.Context, req ResolveRequest) (LeaseTarget, error) {
	client, err := newOVHClient(b.Cfg)
	if err != nil {
		return LeaseTarget{}, err
	}
	// Try direct instance ID lookup
	instance, err := client.GetInstance(ctx, req.ID)
	if err == nil {
		leaseID := blank("", req.ID)
		ip := instance.PublicIPv4()
		target := sshTargetFromConfig(b.Cfg, ip)
		if target.User == b.Cfg.SSHUser {
			target.User = ovhSSHUser
		}
		useStoredTestboxKey(&target, leaseID)
		var server Server
		server.CloudID = instance.ID
		server.Provider = "ovh"
		server.Name = instance.Name
		server.Status = instance.Status
		server.PublicNet.IPv4.IP = ip
		return LeaseTarget{Server: server, SSH: target, LeaseID: leaseID}, nil
	}
	// Fall through to instance list for name/alias matching
	instances, err := client.ListInstances(ctx)
	if err != nil {
		return LeaseTarget{}, err
	}
	for _, inst := range instances {
		if strings.EqualFold(inst.Name, req.ID) || strings.EqualFold(inst.ID, req.ID) {
			ip := inst.PublicIPv4()
			target := sshTargetFromConfig(b.Cfg, ip)
			if target.User == b.Cfg.SSHUser {
				target.User = ovhSSHUser
			}
			var server Server
			server.CloudID = inst.ID
			server.Provider = "ovh"
			server.Name = inst.Name
			server.Status = inst.Status
			server.PublicNet.IPv4.IP = ip
			return LeaseTarget{Server: server, SSH: target, LeaseID: req.ID}, nil
		}
	}
	return LeaseTarget{}, exit(4, "lease/server not found: %s", req.ID)
}

// List returns all OVH instances.
func (b *ovhLeaseBackend) List(ctx context.Context, req ListRequest) ([]LeaseView, error) {
	_ = req
	client, err := newOVHClient(b.Cfg)
	if err != nil {
		return nil, err
	}
	instances, err := client.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var servers []Server
	for _, inst := range instances {
		var s Server
		s.CloudID = inst.ID
		s.Provider = "ovh"
		s.Name = inst.Name
		s.Status = inst.Status
		s.PublicNet.IPv4.IP = inst.PublicIPv4()
		servers = append(servers, s)
	}
	return servers, nil
}

// ReleaseLease terminates an OVH instance.
func (b *ovhLeaseBackend) ReleaseLease(ctx context.Context, req ReleaseLeaseRequest) error {
	client, err := newOVHClient(b.Cfg)
	if err != nil {
		return err
	}
	if err := client.DeleteInstance(ctx, req.Lease.Server.CloudID); err != nil {
		return err
	}
	removeLeaseClaim(req.Lease.LeaseID)
	return nil
}

// Touch updates lease labels. OVH has no native label support, so labels are
// tracked via coordinator/local claim files only.
func (b *ovhLeaseBackend) Touch(ctx context.Context, req TouchRequest) (Server, error) {
	server := req.Lease.Server
	server.Labels = core.TouchDirectLeaseLabels(server.Labels, b.Cfg, req.State, time.Now().UTC())
	return server, nil
}

// Cleanup reaps expired/stale OVH instances.
func (b *ovhLeaseBackend) Cleanup(ctx context.Context, req CleanupRequest) error {
	servers, err := b.List(ctx, ListRequest{Options: req.Options})
	if err != nil {
		return err
	}
	for _, server := range servers {
		shouldDelete, reason := core.ShouldCleanupServer(server, time.Now().UTC())
		if !shouldDelete {
			fmt.Fprintf(b.RT.Stderr, "skip server id=%s name=%s reason=%s\n", server.DisplayID(), server.Name, reason)
			continue
		}
		fmt.Fprintf(b.RT.Stderr, "delete server id=%s name=%s\n", server.DisplayID(), server.Name)
		if req.DryRun {
			continue
		}
		client, err := newOVHClient(b.Cfg)
		if err != nil {
			return err
		}
		if err := client.DeleteInstance(ctx, server.CloudID); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helper stubs (matching GCP/Hetzner provider pattern)
// ---------------------------------------------------------------------------

func exit(code int, format string, args ...any) core.ExitError {
	return core.Exit(code, format, args...)
}

func newOVHClient(cfg Config) (*core.OVHClient, error) {
	return core.NewOVHClient(cfg)
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

func blank(value, fallback string) string { return core.Blank(value, fallback) }

func useStoredTestboxKey(target *SSHTarget, leaseID string) {
	if keyPath, err := core.TestboxKeyPath(leaseID); err == nil {
		if _, statErr := os.Stat(keyPath); statErr == nil {
			target.Key = keyPath
		}
	}
}

func removeLeaseClaim(leaseID string) { core.RemoveLeaseClaim(leaseID) }

func leaseProviderName(leaseID, slug string) string {
	return core.LeaseProviderName(leaseID, slug)
}

func acquireAttemptsRetry(rt Runtime, keep bool, acquire func() (LeaseTarget, error)) (LeaseTarget, error) {
	return shared.AcquireAttemptsRetry(rt, keep, acquire)
}
