package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCleanAbsolutePath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "absolute", input: "/workspace/../workspace/repo", want: "/workspace/repo"},
		{name: "empty", input: "", want: ""},
		{name: "relative", input: "workspace/repo", want: ""},
		{name: "nul", input: "/workspace/repo\x00bad", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanAbsolutePath(tc.input); got != tc.want {
				t.Fatalf("cleanAbsolutePath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCommandEnvFiltersInvalidNames(t *testing.T) {
	env := commandEnv(map[string]string{
		"GOOD_NAME": "kept",
		"1BAD":      "dropped",
		"BAD-NAME":  "dropped",
	})

	if !containsEnv(env, "GOOD_NAME=kept") {
		t.Fatalf("commandEnv missing allowed variable: %#v", env)
	}
	if containsEnv(env, "1BAD=dropped") || containsEnv(env, "BAD-NAME=dropped") {
		t.Fatalf("commandEnv kept invalid variable name: %#v", env)
	}
}

func TestHandleFileUploadWritesDestination(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "nested", "archive.tgz")
	req := httptest.NewRequest(http.MethodPost, "/v1/files?path="+dst, strings.NewReader("payload"))
	rec := httptest.NewRecorder()

	handleFileUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("payload")) {
		t.Fatalf("uploaded data = %q, want payload", data)
	}
}

func TestHandleExecTimeoutCompletesWithExitCode124(t *testing.T) {
	body, err := json.Marshal(execRequest{
		Command:   "sleep 1",
		Cwd:       t.TempDir(),
		TimeoutMS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/exec", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handleExec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	events := parseStreamEvents(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("missing stream events")
	}
	last := events[len(events)-1]
	if last.Type != "complete" || last.ExitCode == nil || *last.ExitCode != 124 {
		t.Fatalf("last event = %#v, want complete exit 124", last)
	}
	for _, event := range events {
		if event.Type == "error" {
			t.Fatalf("unexpected error event: %#v", event)
		}
	}
}

func TestRunCommandMapsSignaledExitCode(t *testing.T) {
	rec := httptest.NewRecorder()
	writer := &eventWriter{w: rec, flusher: rec}

	code, err := runCommand(context.Background(), execRequest{Command: "kill -9 $$"}, t.TempDir(), writer)

	if err != nil {
		t.Fatal(err)
	}
	if code != 137 {
		t.Fatalf("exit code = %d, want 137", code)
	}
}

func TestCopyPipeTreatsClosedPipeAsEOF(t *testing.T) {
	var wg sync.WaitGroup
	rec := httptest.NewRecorder()
	writer := &eventWriter{w: rec, flusher: rec}

	wg.Add(1)
	copyPipe(&wg, closedPipeReader{}, "stdout", writer)

	if body := rec.Body.String(); body != "" {
		t.Fatalf("copyPipe emitted %q for closed pipe", body)
	}
}

type closedPipeReader struct{}

func (closedPipeReader) Read([]byte) (int, error) {
	return 0, os.ErrClosed
}

func parseStreamEvents(t *testing.T, body string) []streamEvent {
	t.Helper()
	var events []streamEvent
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode stream event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func containsEnv(env []string, value string) bool {
	for _, entry := range env {
		if entry == value {
			return true
		}
	}
	return false
}
