package splunk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

type capturedEvents struct {
	mu     sync.Mutex
	events []splunk.RuntimeEvent
}

func (capture *capturedEvents) HandleEvent(_ context.Context, event splunk.RuntimeEvent) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.events = append(capture.events, event)
}

func (capture *capturedEvents) snapshot() []splunk.RuntimeEvent {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]splunk.RuntimeEvent(nil), capture.events...)
}

func TestRuntimeEventsCoverSearchAndResumedOperations(t *testing.T) {
	const jobID = "event-job"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/":
			_, _ = io.WriteString(w, `<response><sid>`+jobID+`</sid></response>`)
		case "/services/search/jobs/" + jobID:
			_, _ = io.WriteString(w, `{"entry":[{"content":{"dispatchState":"DONE","doneProgress":1,"scanCount":12,"eventCount":4,"resultCount":2}}]}`)
		case "/services/search/v2/jobs/" + jobID + "/results":
			http.Error(w, "v2 unavailable", http.StatusNotFound)
		case "/services/search/jobs/" + jobID + "/results/":
			_, _ = io.WriteString(w, `{"results":[{"value":"safe"}]}`)
		case "/services/search/jobs/" + jobID + "/search.log":
			_, _ = io.WriteString(w, "INFO completed in 0.2 seconds\nWARN bounded warning\n")
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	var events capturedEvents
	var logs bytes.Buffer
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "secret-token", HTTPClient: server.Client(), PollInterval: time.Millisecond, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), EventSink: &events})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(context.Background(), "search private-data earliest=-5m", splunk.SearchOptions{SearchLog: splunk.SearchLogModeSummary}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InspectJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobResults(context.Background(), jobID, splunk.JobResultsOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobResultsToFile(context.Background(), jobID, splunk.JobResultsOptions{}, t.TempDir()+"/results.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobSearchLog(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CancelJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}

	captured := events.snapshot()
	seen := make(map[splunk.RuntimeEventKind]bool)
	for index, event := range captured {
		seen[event.Kind] = true
		if event.Sequence != uint64(index+1) || event.Time.IsZero() {
			t.Fatalf("event order/time invalid: %+v", event)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, secret := range []string{"secret-token", server.URL, "private-data", "bounded warning", `"value":"safe"`} {
			if strings.Contains(text, secret) {
				t.Fatalf("event leaked %q: %s", secret, text)
			}
		}
	}
	for _, kind := range []splunk.RuntimeEventKind{splunk.EventJobDispatched, splunk.EventJobStatus, splunk.EventEndpointFallback, splunk.EventDiagnostics, splunk.EventOutputSaved, splunk.EventOperation, splunk.EventCancellation} {
		if !seen[kind] {
			t.Errorf("missing event kind %q in %+v", kind, captured)
		}
	}
	if logs.Len() == 0 {
		t.Fatal("configured logger did not coexist with event sink")
	}
	searchOutcomes := 0
	for _, event := range captured {
		if event.Kind == splunk.EventOperation && event.Operation == "search" {
			searchOutcomes++
		}
	}
	if searchOutcomes != 1 {
		t.Fatalf("search emitted %d final operation events; want 1", searchOutcomes)
	}
}

func TestRuntimeEventDeliveryIsSerializedForConcurrentSearches(t *testing.T) {
	server := newLoggingTestServer(t)
	var events capturedEvents
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client(), PollInterval: time.Millisecond, EventSink: &events})
	if err != nil {
		t.Fatal(err)
	}
	const searches = 8
	var wait sync.WaitGroup
	for index := 0; index < searches; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = client.Search(context.Background(), "search index=_internal earliest=-5m", splunk.SearchOptions{SearchLog: splunk.SearchLogModeSummary})
		}()
	}
	wait.Wait()
	captured := events.snapshot()
	sequences := make([]int, 0, len(captured))
	for _, event := range captured {
		sequences = append(sequences, int(event.Sequence))
	}
	sort.Ints(sequences)
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("event sequence gap/duplicate at %d: %v", index, sequences)
		}
	}
}

func TestRuntimeEventSinkIsSynchronous(t *testing.T) {
	const jobID = "blocking-event"
	server := jobServer(t, jobID, "DONE", nil)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	sink := splunk.EventSinkFunc(func(context.Context, splunk.RuntimeEvent) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-gate
	})
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client(), EventSink: sink})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = client.InspectJob(context.Background(), jobID)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("event sink was not called")
	}
	select {
	case <-done:
		t.Fatal("operation returned before synchronous sink")
	case <-time.After(10 * time.Millisecond):
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation did not resume after sink returned")
	}
}
