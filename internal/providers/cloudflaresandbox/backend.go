package cloudflaresandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func NewCloudflareSandboxBackend(spec ProviderSpec, cfg Config, rt Runtime) Backend {
	cfg.Provider = providerName
	return &cloudflareSandboxBackend{spec: spec, cfg: cfg, rt: rt}
}

type cloudflareSandboxBackend struct {
	spec ProviderSpec
	cfg  Config
	rt   Runtime
}

func (b *cloudflareSandboxBackend) Spec() ProviderSpec { return b.spec }

func (b *cloudflareSandboxBackend) Warmup(ctx context.Context, req WarmupRequest) error {
	started := b.now()
	client, err := newCloudflareSandboxClient(b.cfg, b.rt)
	if err != nil {
		return err
	}
	leaseID, sandbox, slug, err := b.createSandbox(ctx, client, req.Repo, req.Reclaim)
	if err != nil {
		return err
	}
	fmt.Fprintf(b.rt.Stdout, "leased %s slug=%s provider=%s sandbox=%s\n", leaseID, slug, providerName, sandbox.ID)
	if !req.Keep {
		fmt.Fprintf(b.rt.Stderr, "warning: cloudflare-sandbox warmup keeps the sandbox until explicit stop\n")
	}
	total := b.now().Sub(started)
	fmt.Fprintf(b.rt.Stdout, "warmup complete total=%s\n", total.Round(time.Millisecond))
	if req.TimingJSON {
		return writeTimingJSON(b.rt.Stderr, timingReport{
			Provider: providerName,
			LeaseID:  leaseID,
			Slug:     slug,
			TotalMs:  total.Milliseconds(),
			ExitCode: 0,
		})
	}
	return nil
}

func (b *cloudflareSandboxBackend) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if err := rejectCloudflareSandboxSyncOptions(req); err != nil {
		return RunResult{}, err
	}
	workdir, err := cloudflareSandboxWorkdir(b.cfg)
	if err != nil {
		return RunResult{}, err
	}
	started := b.now()
	client, err := newCloudflareSandboxClient(b.cfg, b.rt)
	if err != nil {
		return RunResult{}, err
	}
	leaseID, sandboxID, slug := "", "", ""
	acquired := false
	if req.ID == "" {
		var sandbox cloudflareSandbox
		leaseID, sandbox, slug, err = b.createSandbox(ctx, client, req.Repo, req.Reclaim)
		if err != nil {
			return RunResult{}, err
		}
		sandboxID = sandbox.ID
		fmt.Fprintf(b.rt.Stderr, "leased %s slug=%s provider=%s sandbox=%s\n", leaseID, slug, providerName, sandboxID)
		acquired = true
	} else {
		leaseID, sandboxID, slug, err = b.resolveSandboxID(req.ID)
		if err != nil {
			return RunResult{}, err
		}
	}
	shouldStop := acquired && !req.Keep
	if shouldStop {
		defer func() {
			if !shouldStop {
				return
			}
			if err := client.destroySandbox(context.Background(), sandboxID); err != nil {
				fmt.Fprintf(b.rt.Stderr, "warning: cloudflare-sandbox destroy failed for %s: %v\n", sandboxID, err)
				return
			}
			removeLeaseClaim(leaseID)
		}()
	}

	syncDuration := time.Duration(0)
	syncPhases := []timingPhase{{Name: "sync", Skipped: true, Reason: "--no-sync"}}
	if !req.NoSync {
		syncPhases, syncDuration, err = b.syncWorkspace(ctx, client, sandboxID, req, workdir)
		if err != nil {
			return RunResult{Total: b.now().Sub(started), SyncDelegated: true}, err
		}
		fmt.Fprintf(b.rt.Stderr, "sync complete in %s\n", syncDuration.Round(time.Millisecond))
	} else if err := b.prepareWorkspace(ctx, client, sandboxID, workdir); err != nil {
		return RunResult{}, err
	}
	if req.SyncOnly {
		result := RunResult{Total: b.now().Sub(started), SyncDelegated: true}
		fmt.Fprintf(b.rt.Stdout, "synced %s\n", workdir)
		if req.TimingJSON {
			err := writeTimingJSON(b.rt.Stderr, timingReport{
				Provider:      providerName,
				LeaseID:       leaseID,
				Slug:          slug,
				SyncDelegated: true,
				SyncMs:        syncDuration.Milliseconds(),
				SyncPhases:    syncPhases,
				SyncSkipped:   req.NoSync,
				TotalMs:       result.Total.Milliseconds(),
				ExitCode:      0,
			})
			return result, err
		}
		return result, nil
	}

	command, err := buildCloudflareSandboxCommand(req.Command, req.ShellMode)
	if err != nil {
		return RunResult{}, err
	}
	if req.EnvSummary {
		printEnvForwardingSummary(b.rt.Stderr, providerName, "forwarded", req.Options.EnvAllow, req.Env)
	}
	commandStarted := b.now()
	exitCode, commandErr := client.execStream(ctx, sandboxID, execStreamRequest{
		Command:   command,
		Cwd:       workdir,
		Env:       req.Env,
		TimeoutMS: durationMillisecondsCeil(b.cfg.TTL),
	}, b.rt.Stdout, b.rt.Stderr)
	commandDuration := b.now().Sub(commandStarted)
	result := RunResult{
		ExitCode:      exitCode,
		Command:       commandDuration,
		Total:         b.now().Sub(started),
		SyncDelegated: true,
	}
	if req.NoSync {
		fmt.Fprintf(b.rt.Stderr, "cloudflare-sandbox run summary sync_skipped=true command=%s total=%s exit=%d\n", result.Command.Round(time.Millisecond), result.Total.Round(time.Millisecond), result.ExitCode)
	} else {
		fmt.Fprintf(b.rt.Stderr, "cloudflare-sandbox run summary sync=%s command=%s total=%s exit=%d\n", syncDuration.Round(time.Millisecond), result.Command.Round(time.Millisecond), result.Total.Round(time.Millisecond), result.ExitCode)
	}
	if req.TimingJSON {
		if err := writeTimingJSON(b.rt.Stderr, timingReport{
			Provider:      providerName,
			LeaseID:       leaseID,
			Slug:          slug,
			SyncDelegated: true,
			SyncMs:        syncDuration.Milliseconds(),
			SyncPhases:    syncPhases,
			SyncSkipped:   req.NoSync,
			CommandMs:     commandDuration.Milliseconds(),
			TotalMs:       result.Total.Milliseconds(),
			ExitCode:      result.ExitCode,
		}); err != nil {
			return result, err
		}
	}
	if commandErr != nil {
		handleDelegatedRunFailure(b.rt.Stderr, req, providerName, leaseID, slug, b.cfg.IdleTimeout, b.cfg.TTL, acquired, &shouldStop)
		return result, ExitError{Code: 1, Message: fmt.Sprintf("cloudflare-sandbox run failed: %v", commandErr)}
	}
	if result.ExitCode != 0 {
		handleDelegatedRunFailure(b.rt.Stderr, req, providerName, leaseID, slug, b.cfg.IdleTimeout, b.cfg.TTL, acquired, &shouldStop)
		return result, ExitError{Code: result.ExitCode, Message: fmt.Sprintf("cloudflare-sandbox run exited %d", result.ExitCode)}
	}
	return result, nil
}

func (b *cloudflareSandboxBackend) List(ctx context.Context, req ListRequest) ([]LeaseView, error) {
	_ = ctx
	_ = req
	claims, err := localCloudflareSandboxClaims()
	if err != nil {
		return nil, err
	}
	servers := make([]Server, 0, len(claims))
	for _, claim := range claims {
		servers = append(servers, claimToServer(claim, "unknown"))
	}
	return servers, nil
}

func (b *cloudflareSandboxBackend) Status(ctx context.Context, req StatusRequest) (StatusView, error) {
	client, err := newCloudflareSandboxClient(b.cfg, b.rt)
	if err != nil {
		return StatusView{}, err
	}
	leaseID, sandboxID, slug, err := b.resolveSandboxID(req.ID)
	if err != nil {
		return StatusView{}, err
	}
	deadline := b.now().Add(req.WaitTimeout)
	if req.WaitTimeout <= 0 {
		deadline = b.now().Add(5 * time.Minute)
	}
	for {
		sandbox, err := client.getSandbox(ctx, sandboxID)
		if err != nil {
			return StatusView{}, err
		}
		view := sandboxStatusView(leaseID, slug, sandbox)
		if !req.Wait || view.Ready {
			if cloudflareSandboxTerminalState(view.State) {
				removeLeaseClaim(leaseID)
			}
			return view, nil
		}
		if b.now().After(deadline) {
			return StatusView{}, exit(5, "timed out waiting for cloudflare sandbox %s to become ready", sandboxID)
		}
		select {
		case <-ctx.Done():
			return StatusView{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (b *cloudflareSandboxBackend) Stop(ctx context.Context, req StopRequest) error {
	client, err := newCloudflareSandboxClient(b.cfg, b.rt)
	if err != nil {
		return err
	}
	leaseID, sandboxID, _, err := b.resolveSandboxID(req.ID)
	if err != nil {
		return err
	}
	if err := client.destroySandbox(ctx, sandboxID); err != nil {
		return err
	}
	removeLeaseClaim(leaseID)
	fmt.Fprintf(b.rt.Stdout, "stopped %s provider=%s sandbox=%s\n", leaseID, providerName, sandboxID)
	return nil
}

func (b *cloudflareSandboxBackend) Cleanup(ctx context.Context, req CleanupRequest) error {
	client, err := newCloudflareSandboxClient(b.cfg, b.rt)
	if err != nil {
		return err
	}
	claims, err := localCloudflareSandboxClaims()
	if err != nil {
		return err
	}
	removed := 0
	for _, claim := range claims {
		sandbox, err := client.getSandbox(ctx, claim.LeaseID)
		if err != nil {
			if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
				if req.DryRun {
					fmt.Fprintf(b.rt.Stdout, "would remove stale cloudflare-sandbox claim %s slug=%s reason=not-found\n", claim.LeaseID, blank(claim.Slug, "-"))
					continue
				}
				removeLeaseClaim(claim.LeaseID)
				removed++
				fmt.Fprintf(b.rt.Stdout, "removed stale cloudflare-sandbox claim %s slug=%s reason=not-found\n", claim.LeaseID, blank(claim.Slug, "-"))
				continue
			}
			fmt.Fprintf(b.rt.Stderr, "warning: cloudflare-sandbox status failed for %s: %v\n", claim.LeaseID, err)
			continue
		}
		if !cloudflareSandboxTerminalState(sandbox.State) {
			continue
		}
		if req.DryRun {
			fmt.Fprintf(b.rt.Stdout, "would remove stale cloudflare-sandbox claim %s slug=%s state=%s\n", claim.LeaseID, blank(claim.Slug, "-"), sandbox.State)
			continue
		}
		removeLeaseClaim(claim.LeaseID)
		removed++
		fmt.Fprintf(b.rt.Stdout, "removed stale cloudflare-sandbox claim %s slug=%s state=%s\n", claim.LeaseID, blank(claim.Slug, "-"), sandbox.State)
	}
	if !req.DryRun {
		fmt.Fprintf(b.rt.Stdout, "cloudflare-sandbox cleanup removed=%d checked=%d\n", removed, len(claims))
	}
	return nil
}

func (b *cloudflareSandboxBackend) createSandbox(ctx context.Context, client *cloudflareSandboxClient, repo Repo, reclaim bool) (string, cloudflareSandbox, string, error) {
	leaseID := newLeaseID()
	slug := newLeaseSlug(leaseID)
	workdir, err := cloudflareSandboxWorkdir(b.cfg)
	if err != nil {
		return "", cloudflareSandbox{}, "", err
	}
	labels := map[string]string{
		"crabbox":  "true",
		"provider": providerName,
		"lease":    leaseID,
		"slug":     slug,
		"repo":     repo.Name,
	}
	sandbox, err := client.createSandbox(ctx, createSandboxRequest{
		ID:                 leaseID,
		LeaseID:            leaseID,
		Slug:               slug,
		Repo:               repo.Name,
		Workdir:            workdir,
		TTLSeconds:         durationSecondsCeil(b.cfg.TTL),
		IdleTimeoutSeconds: durationSecondsCeil(b.cfg.IdleTimeout),
		Labels:             labels,
	})
	if err != nil {
		return "", cloudflareSandbox{}, "", err
	}
	if err := claimLeaseForRepoProvider(leaseID, slug, providerName, repo.Root, b.cfg.IdleTimeout, reclaim); err != nil {
		_ = client.destroySandbox(context.Background(), sandbox.ID)
		return "", cloudflareSandbox{}, "", err
	}
	return leaseID, sandbox, slug, nil
}

func (b *cloudflareSandboxBackend) resolveSandboxID(identifier string) (string, string, string, error) {
	claim, ok, err := resolveLeaseClaimForProvider(identifier, providerName)
	if err != nil {
		return "", "", "", err
	}
	if ok {
		return claim.LeaseID, claim.LeaseID, blank(claim.Slug, newLeaseSlug(claim.LeaseID)), nil
	}
	value := strings.TrimSpace(identifier)
	if value == "" {
		return "", "", "", exit(2, "cloudflare-sandbox id is required")
	}
	return value, value, newLeaseSlug(value), nil
}

func buildCloudflareSandboxCommand(command []string, shellMode bool) (string, error) {
	if len(command) == 0 {
		return "", errors.New("missing command")
	}
	if shellMode {
		return strings.Join(command, " "), nil
	}
	if shouldUseShell(command) || leadingEnvAssignment(command) {
		return shellScriptFromArgv(command), nil
	}
	return strings.Join(shellWords(command), " "), nil
}

func rejectCloudflareSandboxSyncOptions(req RunRequest) error {
	if req.ChecksumSync {
		return exit(2, "%s uses archive sync; --checksum is not supported", providerName)
	}
	return nil
}

func cloudflareSandboxWorkdir(cfg Config) (string, error) {
	workdir := blank(strings.TrimSpace(cfg.CloudflareSandbox.Workdir), "/workspace/crabbox")
	clean := path.Clean(workdir)
	if !strings.HasPrefix(clean, "/") {
		return "", exit(2, "cloudflare-sandbox workdir %q must resolve to an absolute path", workdir)
	}
	switch clean {
	case "/", "/bin", "/dev", "/etc", "/home", "/lib", "/lib64", "/opt", "/proc", "/root", "/sbin", "/sys", "/tmp", "/usr", "/var", "/workspace":
		return "", exit(2, "cloudflare-sandbox workdir %q is too broad; choose a dedicated subdirectory", clean)
	}
	return clean, nil
}

func sandboxStatusView(leaseID, slug string, sandbox cloudflareSandbox) StatusView {
	server := sandboxToServer(leaseID, slug, sandbox)
	return StatusView{
		ID:         leaseID,
		Slug:       blank(slug, newLeaseSlug(leaseID)),
		Provider:   providerName,
		TargetOS:   targetLinux,
		State:      server.Status,
		ServerID:   sandbox.ID,
		ServerType: "cloudflare-sandbox",
		Network:    networkPublic,
		Ready:      cloudflareSandboxReady(server.Status),
		Labels:     server.Labels,
	}
}

func sandboxToServer(leaseID, slug string, sandbox cloudflareSandbox) Server {
	labels := map[string]string{}
	for k, v := range sandbox.Labels {
		labels[k] = v
	}
	labels["provider"] = providerName
	labels["lease"] = leaseID
	labels["slug"] = blank(slug, newLeaseSlug(leaseID))
	labels["target"] = targetLinux
	state := blank(sandbox.State, "running")
	labels["state"] = state
	server := Server{
		Provider: providerName,
		CloudID:  sandbox.ID,
		Name:     sandbox.ID,
		Status:   state,
		Labels:   labels,
	}
	server.ServerType.Name = "cloudflare-sandbox"
	return server
}

func claimToServer(claim localClaim, state string) Server {
	labels := map[string]string{
		"provider": providerName,
		"lease":    claim.LeaseID,
		"slug":     blank(claim.Slug, newLeaseSlug(claim.LeaseID)),
		"target":   targetLinux,
		"state":    state,
	}
	server := Server{
		Provider: providerName,
		CloudID:  claim.LeaseID,
		Name:     claim.LeaseID,
		Status:   state,
		Labels:   labels,
	}
	server.ServerType.Name = "cloudflare-sandbox"
	return server
}

func cloudflareSandboxReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "running", "ready", "started", "active", "healthy", "unknown":
		return true
	default:
		return false
	}
}

func cloudflareSandboxTerminalState(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "expired", "stopped", "stopped_with_code", "destroyed", "not_found", "not-found":
		return true
	default:
		return false
	}
}

func durationSecondsCeil(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int((duration + time.Second - 1) / time.Second)
}

func durationMillisecondsCeil(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Millisecond - 1) / time.Millisecond)
}

func (b *cloudflareSandboxBackend) now() time.Time {
	if b.rt.Clock != nil {
		return b.rt.Clock.Now()
	}
	return time.Now()
}

type localClaim struct {
	LeaseID  string `json:"leaseID"`
	Slug     string `json:"slug,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func localCloudflareSandboxClaims() ([]localClaim, error) {
	dir, err := localClaimsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, exit(2, "read claims directory: %v", err)
	}
	var claims []localClaim
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, exit(2, "read claim %s: %v", entry.Name(), err)
		}
		var claim localClaim
		if err := json.Unmarshal(data, &claim); err != nil {
			return nil, exit(2, "parse claim %s: %v", entry.Name(), err)
		}
		if claim.Provider == providerName {
			claims = append(claims, claim)
		}
	}
	return claims, nil
}

func localClaimsDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "crabbox", "claims"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", exit(2, "user state directory is unavailable")
	}
	return filepath.Join(dir, "crabbox", "state", "claims"), nil
}
