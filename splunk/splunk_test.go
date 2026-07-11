package splunk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

	conn := connection{BaseURL: ts.URL}
	_, err := conn.httpGet(context.Background(), ts.URL, nil)
	if err == nil {
		t.Fatal("expected HTTP status error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected status code in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "scheme=http") {
		t.Fatalf("expected sanitized URL in error, got %v", err)
	}
}

func TestSafeURLForLogRemovesSensitiveURLParts(t *testing.T) {
	got := safeURLForLog("https://user:pass@splunk.example.com:8089/services/search/jobs/?token=secret")
	want := "scheme=https host=splunk.example.com:8089 path=/services/search/jobs/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if strings.Contains(got, "user") || strings.Contains(got, "pass") || strings.Contains(got, "token") || strings.Contains(got, "secret") {
		t.Fatalf("sanitized URL leaked sensitive content: %q", got)
	}
}

func TestLoginValidatesAuthTokenWithCurrentContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/authentication/current-context" {
			t.Fatalf("expected current-context auth check, got path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("expected bearer token auth header, got %q", got)
		}
		if got := r.URL.Query().Get("output_mode"); got != "json" {
			t.Fatalf("expected output_mode=json, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"entry":[]}`))
		if err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
	}))
	defer ts.Close()

	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}

	if err := conn.login(context.Background()); err != nil {
		t.Fatalf("expected auth validation success, got %v", err)
	}
}

func TestDispatchQueryToFileReturnsErrorOnMalformedJobResponse(t *testing.T) {
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

	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   10 * time.Second,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.dispatchQueryToFile(context.Background(), &query, output)
	if err == nil {
		t.Fatal("expected XML parse error")
	}
}

func TestDispatchQueryToFileWritesResultsOnDone(t *testing.T) {
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
			w.Header().Set("Content-Type", "application/json")
			response := `{"entry":[{"content":{"dispatchState":"DONE","doneProgress":1,"scanCount":10,"eventCount":2,"resultCount":1}}]}`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			_, err := w.Write([]byte("INFO Search completed in 1.23 seconds\nWARN DispatchThread - non-fatal warning"))
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

	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.dispatchQueryToFile(context.Background(), &query, output)
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
	if query.LogDiagnostics.ExecutionDuration != "1.23 seconds" {
		t.Fatalf("expected execution duration from search.log, got %q", query.LogDiagnostics.ExecutionDuration)
	}
	if !query.SearchLogRead {
		t.Fatal("expected search.log to be read")
	}
	if len(query.LogDiagnostics.Warnings) != 1 {
		t.Fatalf("expected one warning from search.log, got %#v", query.LogDiagnostics.Warnings)
	}
}

func TestDispatchQueryToFileSendsNamespaceWhenSet(t *testing.T) {
	sid := "sid-app"
	resultsPayload := `{"results": []}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("namespace"); got != "security" {
				t.Fatalf("expected namespace 'security', got %q", got)
			}
			w.Header().Set("Content-Type", "application/xml")
			response := `<response><sid>` + sid + `</sid></response>`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("namespace"); got != "security" {
				t.Fatalf("expected namespace 'security', got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			response := `{"entry":[{"content":{"dispatchState":"DONE"}}]}`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("namespace"); got != "security" {
				t.Fatalf("expected namespace 'security', got %q", got)
			}
			_, err := w.Write([]byte("INFO Search completed in 1 seconds"))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/results/"):
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("namespace"); got != "security" {
				t.Fatalf("expected namespace 'security', got %q", got)
			}
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

	conn := connection{
		AuthToken:  "token",
		BaseURL:    ts.URL,
		AppContext: "security",
		Timeout:    5 * time.Second,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.dispatchQueryToFile(context.Background(), &query, output)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestDispatchQueryToFileReturnsErrorOnResultFetchFailure(t *testing.T) {
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
			w.Header().Set("Content-Type", "application/json")
			response := `{"entry":[{"content":{"dispatchState":"DONE"}}]}`
			_, err := w.Write([]byte(response))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			_, err := w.Write([]byte("INFO Search completed in 1 seconds"))
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

	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	output := t.TempDir() + "/out.json"

	err := conn.dispatchQueryToFile(context.Background(), &query, output)
	if err == nil {
		t.Fatal("expected result fetch error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP 500 related error, got %v", err)
	}
}

func TestDispatchQueryWithOptionsSendsDispatchAndResultParams(t *testing.T) {
	sid := "sid-options"
	resultsPayload := `{"results": []}`
	var searchLogCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("earliest_time"); got != "-15m" {
				t.Fatalf("expected earliest_time -15m, got %q", got)
			}
			if got := r.FormValue("latest_time"); got != "now" {
				t.Fatalf("expected latest_time now, got %q", got)
			}
			if got := r.FormValue("max_count"); got != "50000" {
				t.Fatalf("expected max_count 50000, got %q", got)
			}
			if got := r.Form["rf"]; len(got) != 2 || got[0] != "host" || got[1] != "sourcetype" {
				t.Fatalf("expected two rf values, got %#v", got)
			}
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			searchLogCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/results/"):
			if got := r.URL.Query().Get("output_mode"); got != "csv" {
				t.Fatalf("expected output_mode csv, got %q", got)
			}
			if got := r.URL.Query().Get("count"); got != "0" {
				t.Fatalf("expected count 0, got %q", got)
			}
			if got := r.URL.Query().Get("offset"); got != "10" {
				t.Fatalf("expected offset 10, got %q", got)
			}
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

	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	options := dispatchOptions{
		OutputFile: t.TempDir() + "/out.json",
		DispatchParams: map[string][]string{
			"earliest_time": {"-15m"},
			"latest_time":   {"now"},
			"max_count":     {"50000"},
			"rf":            {"host", "sourcetype"},
		},
		ResultParams: map[string][]string{
			"output_mode": {"csv"},
			"count":       {"0"},
			"offset":      {"10"},
		},
		SearchLogMode: SearchLogModeOff,
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if query.SearchLogRead {
		t.Fatal("expected search.log fetch to be disabled")
	}
	if got := searchLogCalls.Load(); got != 0 {
		t.Fatalf("expected no search.log calls, got %d", got)
	}
}

func TestDispatchQueryWithOptionsUsesV2ResultsWhenAvailable(t *testing.T) {
	sid := "sid-v2"
	resultsPayload := `{"results": [{"source":"v2"}]}`
	var v1Calls atomic.Int32
	var v2Calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/services/search/v2/jobs/"+sid+"/results":
			v2Calls.Add(1)
			if got := r.URL.Query().Get("count"); got != "0" {
				t.Fatalf("expected count 0, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(resultsPayload))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/results/"):
			v1Calls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:         t.TempDir() + "/out.json",
		ResultEndpointMode: ResultEndpointAuto,
		ResultParams:       map[string][]string{"count": {"0"}},
		SearchLogMode:      SearchLogModeOff,
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := v2Calls.Load(); got != 1 {
		t.Fatalf("expected one v2 results call, got %d", got)
	}
	if got := v1Calls.Load(); got != 0 {
		t.Fatalf("expected no v1 results call, got %d", got)
	}
	if strings.TrimSpace(string(query.Results)) != resultsPayload {
		t.Fatalf("expected v2 results payload, got %q", string(query.Results))
	}
}

func TestDispatchQueryWithOptionsFallsBackToV1Results(t *testing.T) {
	sid := "sid-v1-fallback"
	resultsPayload := `{"results": [{"source":"v1"}]}`
	var v1Calls atomic.Int32
	var v2Calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/services/search/v2/jobs/"+sid+"/results":
			v2Calls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/results/"):
			v1Calls.Add(1)
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

	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:         t.TempDir() + "/out.json",
		ResultEndpointMode: ResultEndpointAuto,
		SearchLogMode:      SearchLogModeOff,
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := v2Calls.Load(); got != 1 {
		t.Fatalf("expected one v2 results call, got %d", got)
	}
	if got := v1Calls.Load(); got != 1 {
		t.Fatalf("expected one v1 fallback call, got %d", got)
	}
	if strings.TrimSpace(string(query.Results)) != resultsPayload {
		t.Fatalf("expected v1 fallback payload, got %q", string(query.Results))
	}
}

func TestDispatchQueryWithOptionsHonorsV1ResultEndpoint(t *testing.T) {
	sid := "sid-explicit-v1"
	resultsPayload := `{"results": []}`
	var v2Calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/services/search/v2/jobs/"+sid+"/results":
			v2Calls.Add(1)
			w.WriteHeader(http.StatusOK)
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

	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:         t.TempDir() + "/out.json",
		ResultEndpointMode: ResultEndpointV1,
		SearchLogMode:      SearchLogModeOff,
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := v2Calls.Load(); got != 0 {
		t.Fatalf("expected no v2 calls, got %d", got)
	}
}

func TestDispatchQueryWithOptionsExportsUsingV2WhenAvailable(t *testing.T) {
	exportPayload := `{"result":{"source":"v2-export"}}`
	var v1Calls atomic.Int32
	var v2Calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/v2/jobs/export":
			v2Calls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("search"); got != "search index=_internal earliest=-15m | head 1" {
				t.Fatalf("unexpected search value %q", got)
			}
			if got := r.FormValue("output_mode"); got != "json" {
				t.Fatalf("expected default output_mode json, got %q", got)
			}
			_, err := w.Write([]byte(exportPayload))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/export":
			v1Calls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	output := t.TempDir() + "/export.json"
	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:    output,
		ExecutionMode: ExecutionModeExport,
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected export success, got %v", err)
	}
	if got := v2Calls.Load(); got != 1 {
		t.Fatalf("expected one v2 export call, got %d", got)
	}
	if got := v1Calls.Load(); got != 0 {
		t.Fatalf("expected no v1 export call, got %d", got)
	}
	if query.Job.Sid != "" {
		t.Fatalf("expected export mode to not create sid, got %q", query.Job.Sid)
	}
	if query.SearchLogRead {
		t.Fatal("expected export mode to skip search.log")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read export output: %v", err)
	}
	if string(data) != exportPayload {
		t.Fatalf("unexpected export output %q", string(data))
	}
}

func TestDispatchQueryWithOptionsExportsFallbackToV1(t *testing.T) {
	exportPayload := `{"result":{"source":"v1-export"}}`
	var v1Calls atomic.Int32
	var v2Calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/v2/jobs/export":
			v2Calls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/export":
			v1Calls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("count"); got != "0" {
				t.Fatalf("expected result count 0, got %q", got)
			}
			_, err := w.Write([]byte(exportPayload))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	output := t.TempDir() + "/export.json"
	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:    output,
		ExecutionMode: ExecutionModeExport,
		ResultParams:  map[string][]string{"count": {"0"}},
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected export success, got %v", err)
	}
	if got := v2Calls.Load(); got != 1 {
		t.Fatalf("expected one v2 export call, got %d", got)
	}
	if got := v1Calls.Load(); got != 1 {
		t.Fatalf("expected one v1 export call, got %d", got)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read export output: %v", err)
	}
	if string(data) != exportPayload {
		t.Fatalf("unexpected export output %q", string(data))
	}
}

func TestDispatchQueryWithOptionsExportsExplicitV1(t *testing.T) {
	exportPayload := `{"result":{}}`
	var v2Calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/v2/jobs/export":
			v2Calls.Add(1)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/export":
			_, err := w.Write([]byte(exportPayload))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	output := t.TempDir() + "/export.json"
	conn := connection{
		AuthToken: "token",
		BaseURL:   ts.URL,
		Timeout:   5 * time.Second,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:         output,
		ExecutionMode:      ExecutionModeExport,
		ResultEndpointMode: ResultEndpointV1,
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected export success, got %v", err)
	}
	if got := v2Calls.Load(); got != 0 {
		t.Fatalf("expected no v2 export calls, got %d", got)
	}
}

func TestDispatchQueryWithOptionsExportFailurePreservesExistingOutput(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/services/search/v2/jobs/export":
			w.WriteHeader(http.StatusNotFound)
		case "/services/search/jobs/export":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	output := t.TempDir() + "/existing.json"
	const existing = `{"results":[{"source":"last-good-result"}]}`
	if err := os.WriteFile(output, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	conn := connection{AuthToken: "token", BaseURL: ts.URL, Timeout: 5 * time.Second}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	err := conn.dispatchQueryWithOptions(context.Background(), &query, dispatchOptions{
		OutputFile:    output,
		ExecutionMode: ExecutionModeExport,
	})
	if err == nil {
		t.Fatal("expected export failure")
	}
	actual, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(actual) != existing {
		t.Fatalf("failed export changed existing output: %q", actual)
	}
}

func TestDispatchQueryWithOptionsExportErrorDoesNotLeakSensitiveURLParts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, err := w.Write([]byte("export failure"))
		if err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
	}))
	defer ts.Close()

	conn := connection{
		AuthToken: "token",
		BaseURL:   strings.Replace(ts.URL, "://", "://user:pass@", 1),
		Timeout:   5 * time.Second,
	}
	query := queryState{Query: "search index=_internal earliest=-15m | head 1"}
	options := dispatchOptions{
		OutputFile:    t.TempDir() + "/export.json",
		ExecutionMode: ExecutionModeExport,
	}

	err := conn.dispatchQueryWithOptions(context.Background(), &query, options)
	if err == nil {
		t.Fatal("expected export error")
	}
	if strings.Contains(err.Error(), "user") || strings.Contains(err.Error(), "pass") {
		t.Fatalf("expected sanitized URL in error, got %v", err)
	}
}

func TestDispatchQueryWithOptionsSavesSearchLog(t *testing.T) {
	sid := "sid-save-log"
	searchLog := "INFO Search completed in 1 seconds\nWARN DispatchThread - saved warning"
	resultsPayload := `{"results": []}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			_, err := w.Write([]byte(searchLog))
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

	tempDir := t.TempDir()
	searchLogFile := tempDir + "/custom-search.log"
	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	options := dispatchOptions{
		OutputFile:     tempDir + "/out.json",
		SearchLogMode:  SearchLogModeBoth,
		SearchLogFile:  searchLogFile,
		ResultParams:   map[string][]string{"output_mode": {"json"}},
		DispatchParams: map[string][]string{},
	}

	if err := conn.dispatchQueryWithOptions(context.Background(), &query, options); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !query.SearchLogRead {
		t.Fatal("expected search.log to be read")
	}
	if query.SearchLogFile != searchLogFile {
		t.Fatalf("expected saved search log path %q, got %q", searchLogFile, query.SearchLogFile)
	}
	actual, err := os.ReadFile(searchLogFile)
	if err != nil {
		t.Fatalf("read saved search log: %v", err)
	}
	if string(actual) != searchLog {
		t.Fatalf("unexpected saved search log content: %q", string(actual))
	}
	if len(query.LogDiagnostics.Warnings) != 1 {
		t.Fatalf("expected summarized warning, got %#v", query.LogDiagnostics.Warnings)
	}
}

func TestDerivedSearchLogFile(t *testing.T) {
	if got := derivedSearchLogFile("splunkresults.json"); got != "splunkresults.search.log" {
		t.Fatalf("expected derived file, got %q", got)
	}
	if got := derivedSearchLogFile("splunkresults"); got != "splunkresults.search.log" {
		t.Fatalf("expected derived file without extension, got %q", got)
	}
}

func TestJobStatusReturnsErrorOnFailedState(t *testing.T) {
	terminalStates := []string{
		dispatchStateFailed,
		dispatchStateCancelled,
		dispatchStateInternalCancel,
		dispatchStateUserCancel,
		dispatchStateBadInputCancel,
		dispatchStateQuit,
		dispatchStatePause,
		dispatchStatePaused,
	}

	for _, state := range terminalStates {
		t.Run(state, func(t *testing.T) {
			sid := "sid-" + strings.ToLower(state)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				response := fmt.Sprintf(`{"entry":[{"content":{"dispatchState":%q}}]}`, state)
				_, err := w.Write([]byte(response))
				if err != nil {
					t.Fatalf("unexpected write error: %v", err)
				}
			}))
			defer ts.Close()

			conn := connection{
				AuthToken:    "token",
				BaseURL:      ts.URL,
				Timeout:      30 * time.Second,
				PollInterval: time.Millisecond,
			}
			query := queryState{Job: splunkJob{Sid: sid}}
			err := conn.jobStatus(context.Background(), &query)
			if err == nil {
				t.Fatal("expected terminal state error")
			}
			if !strings.Contains(err.Error(), state) {
				t.Fatalf("expected state %s in error, got %v", state, err)
			}
		})
	}
}

func TestJobStatusCancelsRemoteJobOnContextCancelAfterSidExists(t *testing.T) {
	sid := "sid-timeout"
	var controlCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/control") {
			controlCalls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got := r.FormValue("action"); got != "cancel" {
				t.Fatalf("expected cancel action, got %q", got)
			}
			return
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer ts.Close()

	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      30 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Job: splunkJob{Sid: sid}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := conn.jobStatus(ctx, &query)
	if err == nil {
		t.Fatal("expected context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if got := controlCalls.Load(); got != 1 {
		t.Fatalf("expected one remote cancel call, got %d", got)
	}
}

func TestJobStatusDoesNotCancelRemoteJobWithoutSid(t *testing.T) {
	conn := connection{
		AuthToken: "token",
		Timeout:   30 * time.Second,
	}
	query := queryState{}

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

func TestDispatchQueryToFileFetchesSearchLogOnFailedJob(t *testing.T) {
	sid := "sid-failure-log"
	var searchLogCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/services/search/jobs/":
			w.Header().Set("Content-Type", "application/xml")
			_, err := w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid):
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"entry":[{"content":{"dispatchState":"FAILED"}}]}`))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services/search/jobs/"+sid+"/search.log"):
			searchLogCalls.Add(1)
			_, err := w.Write([]byte("ERROR DispatchThread - failed to run search"))
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	conn := connection{
		AuthToken:    "token",
		BaseURL:      ts.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}
	query := queryState{Query: "search index=_internal | head 1"}
	err := conn.dispatchQueryToFile(context.Background(), &query, t.TempDir()+"/out.json")
	if err == nil {
		t.Fatal("expected failed job error")
	}
	if got := searchLogCalls.Load(); got != 1 {
		t.Fatalf("expected one search.log fetch, got %d", got)
	}
	if !query.SearchLogRead {
		t.Fatal("expected search.log to be read")
	}
	if len(query.LogDiagnostics.Errors) != 1 {
		t.Fatalf("expected one diagnostic error from search.log, got %#v", query.LogDiagnostics.Errors)
	}
}

func TestAnalyzeJobLogExtractsDurationAndBoundsDiagnostics(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("INFO DispatchThread - completed in 2.45 seconds\n")
	for i := 0; i < maxDiagnosticLines+5; i++ {
		builder.WriteString("ERROR SearchOperator - ")
		builder.WriteString(strings.Repeat("x", maxDiagnosticLineLength+20))
		builder.WriteString("\n")
	}
	builder.WriteString("WARN DispatchThread - this warning should be bounded by count\n")

	diagnostics := AnalyzeJobLog(builder.String())
	if diagnostics.ExecutionDuration != "2.45 seconds" {
		t.Fatalf("expected duration, got %q", diagnostics.ExecutionDuration)
	}
	if len(diagnostics.Errors) != maxDiagnosticLines {
		t.Fatalf("expected bounded diagnostic error count %d, got %d", maxDiagnosticLines, len(diagnostics.Errors))
	}
	if !strings.Contains(diagnostics.Errors[0], "[truncated]") {
		t.Fatalf("expected long diagnostic line to be truncated, got %q", diagnostics.Errors[0])
	}
	if len(diagnostics.Warnings) != 1 {
		t.Fatalf("expected warning diagnostics to be bounded independently from errors, got %#v", diagnostics.Warnings)
	}
}
