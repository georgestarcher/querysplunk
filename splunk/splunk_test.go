package splunk

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHTTPCallReturnsErrorOnNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, err := w.Write([]byte("bad gateway"))
		if err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
	}))
	defer ts.Close()

	conn := SplunkConnection{BaseURL: ts.URL}
	_, err := conn.httpGet(context.Background(), ts.URL, nil)
	if err == nil {
		t.Fatal("expected HTTP status error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestDispatchQueryReturnsErrorOnMalformedJobResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/services/search/jobs/" {
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte("not-valid-xml"))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	conn := SplunkConnection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   10 * time.Second,
	}
	query := SplunkQuery{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.DispatchQuery(context.Background(), &query, output)
	if err == nil {
		t.Fatal("expected XML parse error")
	}
}

func TestDispatchQueryWritesResultsOnDone(t *testing.T) {
	sid := "sid-123"
	resultsPayload := `{"results": []}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			response := `<response><sid>` + sid + `</sid></response>`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/xml")
			response := `<entry><content><dict><key name="dispatchState">DONE</key></dict></content></entry>`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/results/"):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(resultsPayload))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	conn := SplunkConnection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := SplunkQuery{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.DispatchQuery(context.Background(), &query, output)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("expected output file to be written: %v", err)
	}
	if strings.TrimSpace(string(actual)) != resultsPayload {
		t.Fatalf("unexpected output file contents: %q", string(actual))
	}
}

func TestDispatchQueryReturnsErrorOnResultFetchFailure(t *testing.T) {
	sid := "sid-456"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			response := `<response><sid>` + sid + `</sid></response>`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/xml")
			response := `<entry><content><dict><key name="dispatchState">DONE</key></dict></content></entry>`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/results/"):
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte("fetch failure"))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	conn := SplunkConnection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := SplunkQuery{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.DispatchQuery(context.Background(), &query, output)
	if err == nil {
		t.Fatal("expected result fetch error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP 500 related error, got %v", err)
	}
}

func TestJobStatusReturnsErrorOnFailedState(t *testing.T) {
	sid := "sid-fail"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		response := `<entry><content><dict><key name="dispatchState">FAILED</key></dict></content></entry>`
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
	}))
	defer ts.Close()

	conn := SplunkConnection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   30 * time.Second,
	}
	query := SplunkQuery{Job: SplunkJob{Sid: sid}}
	err := conn.jobStatus(context.Background(), &query)
	if err == nil {
		t.Fatal("expected terminal state error")
	}
	if !strings.Contains(err.Error(), dispatchStateFailed) {
		t.Fatalf("expected failed state in error, got %v", err)
	}
}

func TestJobStatusRespectsContextCancel(t *testing.T) {
	conn := SplunkConnection{
		AuthToken: "token",
		Timeout:   30 * time.Second,
	}
	query := SplunkQuery{Job: SplunkJob{Sid: "sid-timeout"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()

	err := conn.jobStatus(ctx, &query)
	if err == nil {
		t.Fatal("expected context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
