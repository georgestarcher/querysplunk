//go:build integration

package splunk

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDispatchQueryIntegration(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("SPLUNKBASEURL"), "/")
	if baseURL == "" {
		t.Skip("SPLUNKBASEURL is not set")
	}

	username := os.Getenv("SPLUNKUSERNAME")
	password := os.Getenv("SPLUNKPASSWORD")
	token := os.Getenv("SPLUNKTOKEN")
	appContext := strings.TrimSpace(os.Getenv("SPLUNKAPP"))
	queryString := os.Getenv("SPLUNK_INTEGRATION_QUERY")
	if queryString == "" {
		exampleQuery, err := os.ReadFile("../query.txt")
		if err != nil {
			t.Fatalf("read example query.txt: %v", err)
		}
		queryString = string(exampleQuery)
		if strings.TrimSpace(queryString) == "" {
			t.Fatal("example query.txt is empty")
		}
	}
	t.Logf("testing Splunk integration with base URL %s", safeURLForLog(baseURL))

	if token == "" && (username == "" || password == "") {
		t.Skip("provide either SPLUNKTOKEN or both SPLUNKUSERNAME and SPLUNKPASSWORD")
	}

	tlsVerify := true
	if raw := strings.TrimSpace(os.Getenv("SPLUNKTLSVERIFY")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			t.Fatalf("invalid SPLUNKTLSVERIFY value %q: %v", raw, err)
		}
		tlsVerify = parsed
	}

	timeout := 120 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SPLUNKTIMEOUT")); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds <= 0 {
			t.Fatalf("invalid SPLUNKTIMEOUT value %q: must be a positive integer (seconds)", raw)
		}
		timeout = time.Duration(seconds) * time.Second
	}

	client, err := NewClient(Config{
		App:                appContext,
		Username:           username,
		Password:           password,
		Token:              token,
		BaseURL:            baseURL,
		InsecureSkipVerify: !tlsVerify,
		Timeout:            timeout,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := client.Authenticate(ctx); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	var output bytes.Buffer
	result, err := client.SearchTo(ctx, queryString, SearchOptions{SearchLog: SearchLogModeSummary}, &output)
	if err != nil {
		t.Fatalf("dispatch/query failed: %v", err)
	}
	if output.Len() == 0 {
		t.Fatalf("expected non-empty query output")
	}
	if result.Data != nil {
		t.Fatal("expected SearchTo to stream without retaining result data")
	}
	if result.JobID == "" {
		t.Fatal("expected dispatched query to have a search job id")
	}
	if !result.SearchLogRead {
		t.Fatal("expected integration test to fetch search.log for the search job")
	}
	t.Logf("search job id: %s", result.JobID)
	if result.Diagnostics.ExecutionDuration != "" {
		t.Logf("search execution duration from search.log: %s", result.Diagnostics.ExecutionDuration)
	}
	for _, warning := range result.Diagnostics.Warnings {
		t.Logf("search.log warning: %s", warning)
	}
	for _, diagnosticError := range result.Diagnostics.Errors {
		t.Logf("search.log error: %s", diagnosticError)
	}

	status, err := client.InspectJob(ctx, result.JobID)
	if err != nil {
		t.Fatalf("inspect existing job: %v", err)
	}
	if !status.Terminal || !status.Successful || status.State != dispatchStateDone {
		t.Fatalf("unexpected resumed job status: %+v", status)
	}
	waited, err := client.WaitJob(ctx, result.JobID)
	if err != nil || waited.State != dispatchStateDone {
		t.Fatalf("wait existing job: %+v, %v", waited, err)
	}
	var resumedOutput bytes.Buffer
	if _, err := client.JobResultsTo(ctx, result.JobID, JobResultsOptions{}, &resumedOutput); err != nil {
		t.Fatalf("fetch existing job results: %v", err)
	}
	if resumedOutput.Len() == 0 {
		t.Fatal("expected non-empty resumed job results")
	}
	jobLog, err := client.JobSearchLog(ctx, result.JobID)
	if err != nil || strings.TrimSpace(jobLog.Text) == "" {
		t.Fatalf("fetch existing job search.log: bytes=%d error=%v", len(jobLog.Text), err)
	}
	cancellation, err := client.CancelJob(ctx, result.JobID)
	if err != nil || cancellation.Requested {
		t.Fatalf("completed job cancellation should be idempotent: %+v, %v", cancellation, err)
	}
}
