package query_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/georgestarcher/querysplunk/v2/query"
	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestResultHandlingValidationAndCredentialFinding(t *testing.T) {
	t.Parallel()

	config := query.Config{
		Search: "| makeresults",
		ResultHandling: &query.ResultHandling{
			Classification:      query.ResultClassificationSecret,
			ContainsCredentials: true,
			AgentDisplay:        query.AgentDisplayDoNotDisplay,
			RecommendedFileMode: "0600",
			Retention:           query.ResultRetentionTemporary,
		},
		ResultContract: &query.ResultContract{RequiredFields: []string{"value"}, AllowEmpty: true, MaximumRows: 10},
	}
	prepared, err := query.Prepare(config, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	config.ResultHandling.Classification = query.ResultClassificationNormal
	config.ResultContract.RequiredFields[0] = "mutated"
	got := prepared.Config()
	if got.ResultHandling.Classification != query.ResultClassificationSecret || got.ResultContract.RequiredFields[0] != "value" {
		t.Fatalf("prepared result policy aliases caller data: %#v", got)
	}
	got.ResultContract.RequiredFields[0] = "returned-mutation"
	if prepared.Config().ResultContract.RequiredFields[0] != "value" {
		t.Fatal("prepared result contract accessor returned aliased fields")
	}
	found := false
	for _, finding := range prepared.Findings() {
		if finding.Kind == query.FindingResultContainsCredentials {
			found = true
			if strings.Contains(finding.Message, "test-token") {
				t.Fatalf("credential finding contains result material: %q", finding.Message)
			}
		}
	}
	if !found {
		t.Fatal("credential-bearing result warning was not emitted")
	}

	invalid := []query.ResultHandling{
		{Classification: "private", AgentDisplay: query.AgentDisplayNormal, RecommendedFileMode: "0600", Retention: query.ResultRetentionNormal},
		{Classification: query.ResultClassificationNormal, AgentDisplay: "raw", RecommendedFileMode: "0600", Retention: query.ResultRetentionNormal},
		{Classification: query.ResultClassificationSensitive, AgentDisplay: query.AgentDisplaySummaryOnly, RecommendedFileMode: "0644", Retention: query.ResultRetentionTemporary},
		{Classification: query.ResultClassificationSecret, AgentDisplay: query.AgentDisplaySummaryOnly, RecommendedFileMode: "0600", Retention: query.ResultRetentionTemporary},
		{Classification: query.ResultClassificationSensitive, ContainsCredentials: true, AgentDisplay: query.AgentDisplayDoNotDisplay, RecommendedFileMode: "0600", Retention: query.ResultRetentionTemporary},
		{Classification: query.ResultClassificationSecret, ContainsCredentials: true, AgentDisplay: query.AgentDisplayDoNotDisplay, RecommendedFileMode: "0600", Retention: query.ResultRetentionNormal},
	}
	for index := range invalid {
		candidate := query.Config{Search: config.Search, ResultHandling: &invalid[index]}
		if err := candidate.Validate(); !errors.Is(err, query.ErrInvalidConfig) {
			t.Fatalf("invalid handling %d error = %v; want ErrInvalidConfig", index, err)
		}
	}

	badContract := query.Config{Search: config.Search, Results: query.Results{OutputMode: "csv"}, ResultContract: &query.ResultContract{MaximumRows: 1}}
	if err := badContract.Validate(); !errors.Is(err, query.ErrInvalidConfig) {
		t.Fatalf("non-JSON contract error = %v; want ErrInvalidConfig", err)
	}
}

func TestValidateResultContractShapesAndFailures(t *testing.T) {
	t.Parallel()

	prepare := func(t *testing.T, contract query.ResultContract) query.Prepared {
		t.Helper()
		prepared, err := query.Prepare(query.Config{Search: "| makeresults", ResultContract: &contract}, query.Overrides{}, query.SafetyPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}

	tests := []struct {
		name     string
		body     string
		contract query.ResultContract
		rows     int
		kind     query.ResultContractErrorKind
	}{
		{name: "job envelope", body: `{"results":[{"name":"one"},{"name":"two"}]}`, contract: query.ResultContract{RequiredFields: []string{"name"}, MaximumRows: 2}, rows: 2},
		{name: "export frames", body: "{\"result\":{\"name\":\"one\"}}\n{\"result\":{\"name\":\"two\"}}\n", contract: query.ResultContract{RequiredFields: []string{"name"}, MaximumRows: 2}, rows: 2},
		{name: "empty allowed", body: `{"results":[]}`, contract: query.ResultContract{AllowEmpty: true}, rows: 0},
		{name: "empty denied", body: `{"results":[]}`, contract: query.ResultContract{}, kind: query.ResultContractEmpty},
		{name: "empty export allowed", body: "", contract: query.ResultContract{AllowEmpty: true}, rows: 0},
		{name: "empty export denied", body: "", contract: query.ResultContract{}, kind: query.ResultContractEmpty},
		{name: "missing field", body: `{"results":[{"secret":"do-not-leak"}]}`, contract: query.ResultContract{RequiredFields: []string{"name"}}, kind: query.ResultContractMissingField},
		{name: "row limit", body: `{"results":[{"name":"one"},{"name":"two"}]}`, contract: query.ResultContract{MaximumRows: 1}, kind: query.ResultContractRowLimit},
		{name: "invalid JSON", body: `{"results":[`, contract: query.ResultContract{AllowEmpty: true}, kind: query.ResultContractInvalidJSON},
		{name: "invalid shape", body: `{"arbitrary":"do-not-leak"}`, contract: query.ResultContract{AllowEmpty: true}, kind: query.ResultContractInvalidShape},
		{name: "metadata-only envelope", body: `{"messages":[{"type":"WARN","text":"do-not-leak"}],"preview":false}`, contract: query.ResultContract{AllowEmpty: true}, kind: query.ResultContractInvalidShape},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared := prepare(t, test.contract)
			summary, err := prepared.ValidateResult(strings.NewReader(test.body))
			if test.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !summary.Enforced || summary.Rows != test.rows {
					t.Fatalf("summary = %#v; want enforced rows %d", summary, test.rows)
				}
				return
			}
			if !errors.Is(err, query.ErrResultContract) {
				t.Fatalf("error = %v; want ErrResultContract", err)
			}
			var contractError *query.ResultContractError
			if !errors.As(err, &contractError) || contractError.Kind != test.kind {
				t.Fatalf("typed error = %#v; want kind %q", contractError, test.kind)
			}
			if strings.Contains(err.Error(), "do-not-leak") {
				t.Fatalf("contract error leaked a result value: %v", err)
			}
		})
	}
}

func TestSecretSearchToFilePermissionsAndAtomicContract(t *testing.T) {
	responseBody := `{"result":{"savedsearch_name":"example"}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/services/authentication/current-context":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"entry":[]}`))
		case strings.HasSuffix(request.URL.Path, "/jobs/export"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(responseBody))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "test-token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	outputFile := filepath.Join(t.TempDir(), "secret-results.json")
	config := query.Config{
		OutputFile:  outputFile,
		Mode:        "export",
		Search:      "| makeresults",
		Results:     query.Results{OutputMode: "json", Endpoint: "v1"},
		Diagnostics: query.Diagnostics{SearchLog: "off"},
		ResultHandling: &query.ResultHandling{
			Classification:      query.ResultClassificationSecret,
			ContainsCredentials: true,
			AgentDisplay:        query.AgentDisplayDoNotDisplay,
			RecommendedFileMode: "0600",
			Retention:           query.ResultRetentionTemporary,
		},
		ResultContract: &query.ResultContract{RequiredFields: []string{"savedsearch_name"}, MaximumRows: 1},
	}
	prepared, err := query.Prepare(config, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.SearchToFile(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(outputFile)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Fatalf("secret result mode = %04o; want 0600", mode)
		}
	}

	if err := os.WriteFile(outputFile, []byte("previous-result"), 0600); err != nil {
		t.Fatal(err)
	}
	responseBody = `{"result":{"unexpected":"do-not-leak"}}`
	var streamed bytes.Buffer
	if _, err := prepared.SearchTo(context.Background(), client, &streamed); !errors.Is(err, query.ErrResultContract) {
		t.Fatalf("stream contract failure = %v; want ErrResultContract", err)
	}
	if streamed.Len() != 0 {
		t.Fatalf("contract failure exposed %d bytes to the caller", streamed.Len())
	}
	if _, err := prepared.SearchToFile(context.Background(), client); !errors.Is(err, query.ErrResultContract) {
		t.Fatalf("contract failure = %v; want ErrResultContract", err)
	}
	retained, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(retained) != "previous-result" {
		t.Fatalf("contract failure replaced prior output: %q", retained)
	}
}
