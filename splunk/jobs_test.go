package splunk_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestValidateJobID(t *testing.T) {
	valid := []string{"1258421375.19", "mysearch_02151949", "scheduler__user__search__name_at_123", "sid:custom@node-1"}
	for _, jobID := range valid {
		if err := splunk.ValidateJobID(jobID); err != nil {
			t.Errorf("ValidateJobID(%q) = %v", jobID, err)
		}
	}
	invalid := []string{"", " ", " sid", "sid ", "../sid", "sid/child", `sid\child`, "sid?x=1", "sid#fragment", "sid%2Fchild", "sid\nchild", strings.Repeat("a", 513)}
	for _, jobID := range invalid {
		if err := splunk.ValidateJobID(jobID); !errors.Is(err, splunk.ErrInvalidJobID) {
			t.Errorf("ValidateJobID(%q) = %v; want ErrInvalidJobID", jobID, err)
		}
	}
}

func TestJobOperationsValidateBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "")
	ctx := context.Background()
	_, _ = client.InspectJob(ctx, "../job")
	_, _ = client.JobResults(ctx, "../job", splunk.JobResultsOptions{})
	_, _ = client.JobResultsToFile(ctx, "job", splunk.JobResultsOptions{Params: map[string][]string{"namespace": {"other"}}}, "out.json")
	_, _ = client.JobSearchLogToFile(ctx, "job", " ")
	_, _ = client.JobSearchLogToFile(ctx, "job", " output.log ")
	_, _ = client.CancelJob(ctx, "../job")
	if requests.Load() != 0 {
		t.Fatalf("invalid operations made %d requests", requests.Load())
	}
}

func TestInspectAndWaitJob(t *testing.T) {
	const jobID = "1258421375.19"
	var statusCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID:
			if request.URL.Query().Get("namespace") != "security" {
				t.Errorf("namespace = %q", request.URL.Query().Get("namespace"))
			}
			call := statusCalls.Add(1)
			state := "RUNNING"
			if call >= 2 {
				state = "DONE"
			}
			_, _ = fmt.Fprintf(w, `{"entry":[{"content":{"dispatchState":%q,"doneProgress":0.75,"scanCount":"12","eventCount":8,"resultCount":"3"}}]}`, state)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "security")

	status, err := client.InspectJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "RUNNING" || status.Terminal || status.DoneProgress != 0.75 || status.ScanCount != 12 || status.EventCount != 8 || status.ResultCount != 3 {
		t.Fatalf("unexpected status: %+v", status)
	}
	status, err = client.WaitJob(context.Background(), jobID)
	if err != nil || !status.Terminal || !status.Successful || status.State != "DONE" {
		t.Fatalf("WaitJob = %+v, %v", status, err)
	}
}

func TestWaitJobCancellationDoesNotCancelRemote(t *testing.T) {
	const jobID = "running-job"
	var controls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID:
			_, _ = io.WriteString(w, `{"entry":[{"content":{"dispatchState":"RUNNING"}}]}`)
		case "/services/search/jobs/" + jobID + "/control":
			controls.Add(1)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	status, err := client.WaitJob(ctx, jobID)
	if !errors.Is(err, context.DeadlineExceeded) || status.State != "RUNNING" || controls.Load() != 0 {
		t.Fatalf("WaitJob = %+v, %v; controls=%d", status, err, controls.Load())
	}
}

func TestWaitJobReturnsTerminalStateError(t *testing.T) {
	const jobID = "failed-job"
	server := jobServer(t, jobID, "FAILED", nil)
	client := newJobClient(t, server, time.Millisecond, "")
	status, err := client.WaitJob(context.Background(), jobID)
	var stateErr *splunk.JobStateError
	if !errors.As(err, &stateErr) || status.State != "FAILED" || !status.Terminal || status.Successful {
		t.Fatalf("WaitJob = %+v, %#v", status, err)
	}
}

func TestJobResultsFallbackStreamingAndAtomicFile(t *testing.T) {
	const jobID = "results-job"
	var v2Calls, v1Calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/v2/jobs/" + jobID + "/results":
			v2Calls.Add(1)
			http.Error(w, "v2 unavailable", http.StatusNotFound)
		case "/services/search/jobs/" + jobID + "/results/":
			v1Calls.Add(1)
			if request.URL.Query().Get("output_mode") != "json" || request.URL.Query().Get("count") != "10" {
				t.Errorf("result query = %q", request.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"results":[{"value":"current"}]}`)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "")
	options := splunk.JobResultsOptions{Params: map[string][]string{"count": {"10"}}}
	result, err := client.JobResults(context.Background(), jobID, options)
	if err != nil || result.JobID != jobID || result.State != "DONE" || !bytes.Contains(result.Data, []byte("current")) || v2Calls.Load() != 1 || v1Calls.Load() != 1 {
		t.Fatalf("JobResults = %+v, %v; calls=%d/%d", result, err, v2Calls.Load(), v1Calls.Load())
	}

	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobResultsToFile(context.Background(), jobID, options, path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("current")) {
		t.Fatalf("atomic result = %q", data)
	}

	writer := &failingWriter{}
	if _, err := client.JobResultsTo(context.Background(), jobID, options, writer); err == nil {
		t.Fatal("expected writer failure")
	}
}

func TestJobResultsFailurePreservesExistingFile(t *testing.T) {
	const jobID = "result-failure"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/v2/jobs/" + jobID + "/results":
			http.Error(w, "failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "")
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte("last-good"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobResultsToFile(context.Background(), jobID, splunk.JobResultsOptions{}, path); err == nil {
		t.Fatal("expected result failure")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "last-good" {
		t.Fatalf("failed retrieval replaced output: %q", data)
	}
}

func TestJobSearchLogAndAtomicFile(t *testing.T) {
	const jobID = "log-job"
	const rawLog = "INFO Search completed in 0.25 seconds\nWARN synthetic warning\nERROR synthetic error\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID + "/search.log":
			_, _ = io.WriteString(w, rawLog)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "")
	jobLog, err := client.JobSearchLog(context.Background(), jobID)
	if err != nil || jobLog.Text != rawLog || jobLog.Diagnostics.ExecutionDuration != "0.25 seconds" || len(jobLog.Diagnostics.Warnings) != 1 || len(jobLog.Diagnostics.Errors) != 1 {
		t.Fatalf("JobSearchLog = %+v, %v", jobLog, err)
	}
	path := filepath.Join(t.TempDir(), "search.log")
	if _, err := client.JobSearchLogToFile(context.Background(), jobID, path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != rawLog {
		t.Fatalf("saved search.log changed: %q", data)
	}
}

func TestCancelJobActiveAndTerminal(t *testing.T) {
	for _, test := range []struct {
		name          string
		state         string
		wantRequested bool
		wantControls  int32
	}{
		{name: "active", state: "RUNNING", wantRequested: true, wantControls: 1},
		{name: "paused", state: "PAUSED", wantRequested: true, wantControls: 1},
		{name: "terminal", state: "DONE", wantRequested: false, wantControls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			const jobID = "cancel-job"
			var controls atomic.Int32
			server := jobServer(t, jobID, test.state, &controls)
			client := newJobClient(t, server, time.Millisecond, "")
			result, err := client.CancelJob(context.Background(), jobID)
			if err != nil || result.Requested != test.wantRequested || controls.Load() != test.wantControls {
				t.Fatalf("CancelJob = %+v, %v; controls=%d", result, err, controls.Load())
			}
		})
	}
}

func TestCancelJobHandlesCompletionRace(t *testing.T) {
	const jobID = "cancel-race"
	var statusCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID:
			state := "RUNNING"
			if statusCalls.Add(1) > 1 {
				state = "DONE"
			}
			_, _ = fmt.Fprintf(w, `{"entry":[{"content":{"dispatchState":%q}}]}`, state)
		case "/services/search/jobs/" + jobID + "/control":
			http.Error(w, "job already completed", http.StatusBadRequest)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := newJobClient(t, server, time.Millisecond, "")
	result, err := client.CancelJob(context.Background(), jobID)
	if err != nil || result.Requested || !result.Job.Successful {
		t.Fatalf("CancelJob race = %+v, %v", result, err)
	}
}

func newJobClient(t *testing.T, server *httptest.Server, poll time.Duration, app string) *splunk.Client {
	t.Helper()
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "synthetic-token", HTTPClient: server.Client(), PollInterval: poll, App: app})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func jobServer(t *testing.T, jobID, state string, controls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID:
			_, _ = fmt.Fprintf(w, `{"entry":[{"content":{"dispatchState":%q}}]}`, state)
		case "/services/search/jobs/" + jobID + "/control":
			if controls != nil {
				controls.Add(1)
			}
			if err := request.ParseForm(); err != nil || request.FormValue("action") != "cancel" {
				t.Errorf("cancel form = %v, %v", request.Form, err)
			}
			_, _ = io.WriteString(w, `<response/>`)
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
