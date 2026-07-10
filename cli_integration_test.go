//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplunkdHealthConfigIntegration(t *testing.T) {
	if strings.TrimSpace(os.Getenv("SPLUNKBASEURL")) == "" {
		t.Skip("SPLUNKBASEURL is not set")
	}
	if strings.TrimSpace(os.Getenv("SPLUNKTOKEN")) == "" && (strings.TrimSpace(os.Getenv("SPLUNKUSERNAME")) == "" || strings.TrimSpace(os.Getenv("SPLUNKPASSWORD")) == "") {
		t.Skip("provide either SPLUNKTOKEN or both SPLUNKUSERNAME and SPLUNKPASSWORD")
	}

	outputFile := filepath.Join(t.TempDir(), "splunkd-health.json")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, "go", "run", ".", "-config", "examples/health/splunkd-health.yml", "-o", outputFile)
	command.Env = os.Environ()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		t.Fatalf("run splunkd health config: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read splunkd health output: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty splunkd health output")
	}

	t.Logf("splunkd health output bytes: %d", len(data))
	logResultSummary(t, data)
}

func logResultSummary(t *testing.T, data []byte) {
	t.Helper()

	var payload struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Logf("splunkd health output is not a JSON results envelope: %v", err)
		return
	}
	t.Logf("splunkd health result count: %d", len(payload.Results))
	if len(payload.Results) == 0 {
		return
	}

	preview := make(map[string]any)
	for _, key := range []string{"title", "health", "status", "message"} {
		if value, ok := payload.Results[0][key]; ok {
			preview[key] = value
		}
	}
	if len(preview) > 0 {
		t.Logf("splunkd health first result summary: %#v", preview)
	}
}
