package splunk_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestClientDefaultLoggerIsQuiet(t *testing.T) {
	server := newLoggingTestServer(t)

	var standardLog bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&standardLog)
	t.Cleanup(func() { log.SetOutput(originalOutput) })

	client, err := splunk.NewClient(splunk.Config{
		BaseURL:      server.URL,
		Token:        "sensitive-token",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Splunk client: %v", err)
		}
	})

	_, err = client.Search(context.Background(), "search index=private earliest=-5m secret-spl", splunk.SearchOptions{SearchLog: splunk.SearchLogModeSummary})
	if err != nil {
		t.Fatal(err)
	}
	if standardLog.Len() != 0 {
		t.Fatalf("default client wrote to the process logger: %q", standardLog.String())
	}
}

func TestClientStructuredLoggerIsSafeAndBounded(t *testing.T) {
	server := newLoggingTestServer(t)
	var logs bytes.Buffer
	client, err := splunk.NewClient(splunk.Config{
		BaseURL:      server.URL,
		Token:        "sensitive-token",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
		Logger:       slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Splunk client: %v", err)
		}
	})

	result, err := client.Search(context.Background(), "search index=private earliest=-5m secret-spl", splunk.SearchOptions{SearchLog: splunk.SearchLogModeSummary})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics.Warnings) != 20 {
		t.Fatalf("bounded warning count = %d; want 20", len(result.Diagnostics.Warnings))
	}

	output := logs.String()
	for _, expected := range []string{`"job_id":"job-`, `"state":"DONE"`, `"done_progress":"1"`, `"execution_duration":"0.25 seconds"`, `"severity":"warning"`, `"count":20`, `"from_endpoint":"v2"`, `"to_endpoint":"v1"`} {
		if !strings.Contains(output, expected) {
			t.Errorf("structured logs missing %q: %s", expected, output)
		}
	}
	for _, sensitive := range []string{"sensitive-token", "secret-spl", "private.example", "sensitive diagnostic details", server.URL} {
		if strings.Contains(output, sensitive) {
			t.Errorf("structured logs exposed sensitive value %q: %s", sensitive, output)
		}
	}
}

func TestClientLoggerLevelAndConcurrentSearches(t *testing.T) {
	server := newLoggingTestServer(t)
	var logs bytes.Buffer
	client, err := splunk.NewClient(splunk.Config{
		BaseURL:      server.URL,
		Token:        "token",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
		Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Splunk client: %v", err)
		}
	})

	const searches = 8
	var group sync.WaitGroup
	errorsFound := make(chan error, searches)
	for range searches {
		group.Add(1)
		go func() {
			defer group.Done()
			_, searchErr := client.Search(context.Background(), "search index=_internal earliest=-5m", splunk.SearchOptions{SearchLog: splunk.SearchLogModeSummary})
			errorsFound <- searchErr
		}()
	}
	group.Wait()
	close(errorsFound)
	for searchErr := range errorsFound {
		if searchErr != nil {
			t.Fatal(searchErr)
		}
	}

	output := logs.String()
	if strings.Contains(output, `"level":"INFO"`) {
		t.Fatalf("warning-level logger retained informational progress: %s", output)
	}
	if count := strings.Count(output, `"severity":"warning"`); count != searches {
		t.Fatalf("warning records = %d; want %d: %s", count, searches, output)
	}
}

func newLoggingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	var nextSID atomic.Uint64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/services/authentication/current-context":
			_, _ = writer.Write([]byte(`{"entry":[{}]}`))
		case request.URL.Path == "/services/search/jobs/":
			sid := nextSID.Add(1)
			_, _ = fmt.Fprintf(writer, "<response><sid>job-%d</sid></response>", sid)
		case strings.HasSuffix(request.URL.Path, "/search.log"):
			_, _ = writer.Write([]byte(loggingSearchLog()))
		case strings.HasPrefix(request.URL.Path, "/services/search/v2/jobs/") && strings.HasSuffix(request.URL.Path, "/results"):
			http.Error(writer, "private.example sensitive diagnostic details", http.StatusNotFound)
		case strings.HasPrefix(request.URL.Path, "/services/search/jobs/") && strings.HasSuffix(request.URL.Path, "/results/"):
			_, _ = writer.Write([]byte(`{"results":[{"count":"1"}]}`))
		case strings.HasPrefix(request.URL.Path, "/services/search/jobs/"):
			_, _ = writer.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE","doneProgress":1,"scanCount":2,"eventCount":1,"resultCount":1}}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func loggingSearchLog() string {
	lines := []string{"INFO Search completed in 0.25 seconds"}
	for index := range 30 {
		lines = append(lines, fmt.Sprintf("WARN sensitive diagnostic details %d", index))
	}
	return strings.Join(lines, "\n")
}
