package splunk_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	tests := []splunk.Config{
		{},
		{BaseURL: "splunk.example.com", Token: "token"},
		{BaseURL: "https://splunk.example.com", Token: "token", Timeout: -time.Second},
		{BaseURL: "https://splunk.example.com", Token: "token", HTTPClient: http.DefaultClient, Timeout: time.Second},
	}
	for _, config := range tests {
		_, err := splunk.NewClient(config)
		if !errors.Is(err, splunk.ErrInvalidConfig) {
			t.Fatalf("NewClient(%+v) error = %v; want ErrInvalidConfig", config, err)
		}
	}
}

func TestClientSearchAndTypedErrors(t *testing.T) {
	const sid = "consumer-example"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/services/authentication/current-context":
			_, _ = w.Write([]byte(`{"entry":[{}]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/services/search/jobs/":
			_, _ = w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
		case request.URL.Path == "/services/search/jobs/"+sid:
			_, _ = w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
		case request.URL.Path == "/services/search/jobs/"+sid+"/search.log":
			_, _ = w.Write([]byte("INFO Search completed in 0.1 seconds"))
		case request.URL.Path == "/services/search/v2/jobs/"+sid+"/results":
			_, _ = w.Write([]byte(`{"results":[{"count":"1"}]}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := splunk.NewClient(splunk.Config{
		BaseURL:      server.URL,
		Token:        "not-a-real-token",
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

	result, err := client.Search(context.Background(), "search index=_internal earliest=-5m | head 1", splunk.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.JobID != sid || !strings.Contains(string(result.Data), `"count":"1"`) {
		t.Fatalf("unexpected result: %+v", result)
	}

	_, err = client.Search(context.Background(), " ", splunk.SearchOptions{})
	if !errors.Is(err, splunk.ErrEmptySearch) {
		t.Fatalf("empty search error = %v; want ErrEmptySearch", err)
	}
}

type failingWriter struct {
	written int
}

func (writer *failingWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	writer.written++
	return 0, fmt.Errorf("synthetic writer failure")
}

func TestClientSearchToStreamsAndReturnsWriterError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = w.Write([]byte(`{"entry":[{}]}`))
		case "/services/search/v2/jobs/export":
			_, _ = w.Write([]byte(strings.Repeat("result\n", 1024)))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	result, err := client.SearchTo(context.Background(), "search index=_internal earliest=-5m", splunk.SearchOptions{ExecutionMode: splunk.ExecutionModeExport}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data != nil || output.Len() == 0 || result.State != "DONE" {
		t.Fatalf("unexpected streamed result: %+v bytes=%d", result, output.Len())
	}

	writer := &failingWriter{}
	_, err = client.SearchTo(context.Background(), "search index=_internal earliest=-5m", splunk.SearchOptions{ExecutionMode: splunk.ExecutionModeExport}, writer)
	if err == nil || !strings.Contains(err.Error(), "synthetic writer failure") || writer.written == 0 {
		t.Fatalf("writer failure not preserved: err=%v writes=%d", err, writer.written)
	}
}

func TestClientSearchPreservesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Search(ctx, "search index=_internal earliest=-5m | head 1", splunk.SearchOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Search error = %v; want context.Canceled", err)
	}
}
