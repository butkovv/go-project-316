package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

func TestCLIPrintsOnlyJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ".", "--depth=0", server.URL)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("CLI failed: %v\nstderr: %s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("CLI wrote to stderr: %s", stderr.String())
	}

	out := stdout.Bytes()
	if !bytes.HasPrefix(out, []byte("{")) || !bytes.HasSuffix(out, []byte("}")) {
		t.Fatalf("CLI output contains extra text or newline: %q", stdout.String())
	}

	var report map[string]any
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("CLI output is not JSON: %v\noutput: %s", err, stdout.String())
	}
	if report["root_url"] != server.URL {
		t.Fatalf("root_url = %v, want %s", report["root_url"], server.URL)
	}
}
