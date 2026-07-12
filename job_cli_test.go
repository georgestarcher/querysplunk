package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestValidateJobMode(t *testing.T) {
	valid := []jobCLIOptions{
		{},
		{Action: jobActionStatus, JobID: "1258421375.19"},
		{Action: jobActionWait, JobID: "job"},
		{Action: jobActionResults, JobID: "job", OutputExplicit: true},
		{Action: jobActionSearchLog, JobID: "job", OutputExplicit: true},
		{Action: jobActionCancel, JobID: "job"},
	}
	for _, options := range valid {
		active, err := validateJobMode(options, map[string]bool{}, "", "", "")
		if err != nil || active != (options.Action != "" || options.JobID != "") {
			t.Errorf("validateJobMode(%+v) = %v, %v", options, active, err)
		}
	}

	invalid := []struct {
		options jobCLIOptions
		flags   map[string]bool
		config  string
		write   string
	}{
		{options: jobCLIOptions{Action: jobActionStatus}},
		{options: jobCLIOptions{JobID: "job"}},
		{options: jobCLIOptions{Action: "delete", JobID: "job"}},
		{options: jobCLIOptions{Action: jobActionStatus, JobID: "../job"}},
		{options: jobCLIOptions{Action: jobActionStatus, JobID: "job"}, config: "search.yml"},
		{options: jobCLIOptions{Action: jobActionStatus, JobID: "job"}, write: "search.yml"},
		{options: jobCLIOptions{Action: jobActionStatus, JobID: "job"}, flags: map[string]bool{"q": true}},
		{options: jobCLIOptions{Action: jobActionStatus, JobID: "job"}, flags: map[string]bool{"earliest": true}},
		{options: jobCLIOptions{Action: jobActionStatus, JobID: "job", OutputExplicit: true}},
	}
	for _, test := range invalid {
		if _, err := validateJobMode(test.options, test.flags, test.config, "", test.write); err == nil {
			t.Errorf("validateJobMode(%+v, %v) accepted conflict", test.options, test.flags)
		}
	}
}

func TestRunJobActions(t *testing.T) {
	const jobID = "cli-job"
	const searchLog = "INFO completed in 0.1 seconds\nWARN synthetic warning\n"
	var controls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID:
			_, _ = io.WriteString(w, `{"entry":[{"content":{"dispatchState":"DONE","resultCount":1}}]}`)
		case "/services/search/v2/jobs/" + jobID + "/results":
			_, _ = io.WriteString(w, `{"results":[{"value":"current"}]}`)
		case "/services/search/jobs/" + jobID + "/search.log":
			_, _ = io.WriteString(w, searchLog)
		case "/services/search/jobs/" + jobID + "/control":
			controls.Add(1)
			_, _ = io.WriteString(w, `<response/>`)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client(), PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	for _, action := range []string{jobActionStatus, jobActionWait, jobActionCancel} {
		var output bytes.Buffer
		if err := runJobAction(context.Background(), client, jobCLIOptions{Action: action, JobID: jobID}, &output); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
			t.Fatalf("%s JSON: %v\n%s", action, err, output.String())
		}
	}
	if controls.Load() != 0 {
		t.Fatalf("terminal cancel posted control action: %d", controls.Load())
	}

	resultPath := filepath.Join(t.TempDir(), "results.json")
	var resultSummary bytes.Buffer
	if err := runJobAction(context.Background(), client, jobCLIOptions{Action: jobActionResults, JobID: jobID, OutputFile: resultPath, OutputExplicit: true}, &resultSummary); err != nil {
		t.Fatal(err)
	}
	resultData, _ := os.ReadFile(resultPath)
	if !bytes.Contains(resultData, []byte("current")) || !strings.Contains(resultSummary.String(), `"operation": "results"`) {
		t.Fatalf("result=%q summary=%s", resultData, resultSummary.String())
	}

	var rawLog bytes.Buffer
	if err := runJobAction(context.Background(), client, jobCLIOptions{Action: jobActionSearchLog, JobID: jobID}, &rawLog); err != nil || rawLog.String() != searchLog {
		t.Fatalf("raw search log=%q error=%v", rawLog.String(), err)
	}
	logPath := filepath.Join(t.TempDir(), "search.log")
	var logSummary bytes.Buffer
	if err := runJobAction(context.Background(), client, jobCLIOptions{Action: jobActionSearchLog, JobID: jobID, OutputFile: logPath, OutputExplicit: true}, &logSummary); err != nil {
		t.Fatal(err)
	}
	logData, _ := os.ReadFile(logPath)
	if string(logData) != searchLog || !strings.Contains(logSummary.String(), `"execution_duration": "0.1 seconds"`) {
		t.Fatalf("saved log=%q summary=%s", logData, logSummary.String())
	}
}

func TestRunJobWaitDoesNotPrintMissingSnapshot(t *testing.T) {
	const jobID = "expired-job"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID:
			http.NotFound(w, request)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client(), PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runJobAction(context.Background(), client, jobCLIOptions{Action: jobActionWait, JobID: jobID}, &output)
	if err == nil || output.Len() != 0 {
		t.Fatalf("wait error=%v output=%q; want error and empty output", err, output.String())
	}
}
