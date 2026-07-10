//go:build integration

package splunk

import (
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

	conn := SplunkConnection{
		AppContext: appContext,
		Username:   username,
		Password:   password,
		AuthToken:  token,
		BaseURL:    baseURL,
		TLSVerify:  tlsVerify,
		Timeout:    timeout,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := conn.Login(ctx); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	query := SplunkQuery{Query: queryString}
	output := t.TempDir() + "/splunkresults.json"
	if err := conn.DispatchQuery(ctx, &query, output); err != nil {
		t.Fatalf("dispatch/query failed: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty query output")
	}
	if query.Job.Sid == "" {
		t.Fatal("expected dispatched query to have a search job id")
	}
	if !query.SearchLogRead {
		t.Fatal("expected integration test to fetch search.log for the search job")
	}
	t.Logf("search job id: %s", query.Job.Sid)
	if query.LogDiagnostics.ExecutionDuration != "" {
		t.Logf("search execution duration from search.log: %s", query.LogDiagnostics.ExecutionDuration)
	}
	for _, warning := range query.LogDiagnostics.Warnings {
		t.Logf("search.log warning: %s", warning)
	}
	for _, diagnosticError := range query.LogDiagnostics.Errors {
		t.Logf("search.log error: %s", diagnosticError)
	}
}
