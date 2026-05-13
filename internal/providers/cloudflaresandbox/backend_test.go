package cloudflaresandbox

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloudflareSandboxProviderSpec(t *testing.T) {
	spec := Provider{}.Spec()
	if spec.Name != providerName {
		t.Fatalf("spec.Name = %q, want %q", spec.Name, providerName)
	}
	if spec.Kind != "delegated-run" {
		t.Fatalf("spec.Kind = %q, want delegated-run", spec.Kind)
	}
	if len(spec.Features) != 1 || spec.Features[0] != "archive-sync" {
		t.Fatalf("spec.Features = %#v, want archive-sync", spec.Features)
	}
}

func TestCloudflareSandboxWorkdirRejectsBroadPaths(t *testing.T) {
	cfg := Config{}
	cfg.CloudflareSandbox.Workdir = "/workspace"
	if _, err := cloudflareSandboxWorkdir(cfg); err == nil {
		t.Fatal("cloudflareSandboxWorkdir accepted broad /workspace path")
	}
}

func TestBuildCloudflareSandboxCommandQuotesArgv(t *testing.T) {
	got, err := buildCloudflareSandboxCommand([]string{"node", "-e", "console.log('ok')"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "'node' '-e' 'console.log('\\''ok'\\'')'"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestCloudflareSandboxHealthyStateIsReady(t *testing.T) {
	if !cloudflareSandboxReady("healthy") {
		t.Fatal("healthy state should be ready")
	}
}

func TestCloudflareSandboxTokenFlagDoesNotDefaultToConfiguredSecret(t *testing.T) {
	cfg := Config{}
	cfg.CloudflareSandbox.Token = "secret-token"
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	values := RegisterCloudflareSandboxProviderFlags(fs, cfg).(cloudflareSandboxFlagValues)
	if got := *values.Token; got != "" {
		t.Fatalf("token flag default = %q, want empty", got)
	}
}

func TestCloudflareSandboxClientExecStream(t *testing.T) {
	var token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"type":"stdout","data":"hello\n"}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"stderr","data":"warn\n"}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"complete","exitCode":7}` + "\n"))
	}))
	defer server.Close()

	token = "test-token"
	cfg := Config{}
	cfg.CloudflareSandbox.APIURL = server.URL
	cfg.CloudflareSandbox.Token = token
	client, err := newCloudflareSandboxClient(cfg, Runtime{HTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code, err := client.execStream(context.Background(), "cbx_test", execStreamRequest{Command: "true"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "warn\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDurationCeil(t *testing.T) {
	if got := durationMillisecondsCeil(1500 * time.Microsecond); got != 2 {
		t.Fatalf("durationMillisecondsCeil = %d, want 2", got)
	}
}

func TestCloudflareSandboxStatusPrunesExpiredClaim(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sandboxes/cbx_expired" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"cbx_expired","state":"expired","workdir":"/workspace/repo"}`)
	}))
	defer server.Close()

	if err := claimLeaseForRepoProvider("cbx_expired", "blue-lobster", providerName, t.TempDir(), time.Hour, false); err != nil {
		t.Fatal(err)
	}
	backend := cloudflareSandboxBackend{
		cfg: Config{
			Provider: providerName,
			CloudflareSandbox: CloudflareSandboxConfig{
				APIURL: server.URL,
				Token:  "token",
			},
		},
		rt: Runtime{HTTP: server.Client()},
	}
	view, err := backend.Status(context.Background(), StatusRequest{ID: "blue-lobster"})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != "expired" {
		t.Fatalf("state = %q, want expired", view.State)
	}
	if _, ok, err := resolveLeaseClaimForProvider("blue-lobster", providerName); err != nil || ok {
		t.Fatalf("claim resolved after expired status ok=%t err=%v", ok, err)
	}
}

func TestCloudflareSandboxCleanupPrunesTerminalClaims(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sandboxes/cbx_expired":
			_, _ = fmt.Fprint(w, `{"id":"cbx_expired","state":"expired","workdir":"/workspace/repo"}`)
		case "/v1/sandboxes/cbx_running":
			_, _ = fmt.Fprint(w, `{"id":"cbx_running","state":"running","workdir":"/workspace/repo"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	repo := t.TempDir()
	if err := claimLeaseForRepoProvider("cbx_expired", "blue-lobster", providerName, repo, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if err := claimLeaseForRepoProvider("cbx_running", "green-lobster", providerName, repo, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	backend := cloudflareSandboxBackend{
		cfg: Config{
			Provider: providerName,
			CloudflareSandbox: CloudflareSandboxConfig{
				APIURL: server.URL,
				Token:  "token",
			},
		},
		rt: Runtime{HTTP: server.Client(), Stdout: &stdout},
	}
	if err := backend.Cleanup(context.Background(), CleanupRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := resolveLeaseClaimForProvider("blue-lobster", providerName); err != nil || ok {
		t.Fatalf("expired claim resolved after cleanup ok=%t err=%v", ok, err)
	}
	if _, ok, err := resolveLeaseClaimForProvider("green-lobster", providerName); err != nil || !ok {
		t.Fatalf("running claim missing after cleanup ok=%t err=%v", ok, err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("removed=1 checked=2")) {
		t.Fatalf("cleanup output = %q, want removed summary", stdout.String())
	}
}
