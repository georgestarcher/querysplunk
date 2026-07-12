package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	querypkg "github.com/georgestarcher/querysplunk/v2/query"
	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestValidateSearchConfigOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.yml")
	if err := os.WriteFile(path, []byte("search: search index=_internal earliest=-15m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLUNKBASEURL", "")
	t.Setenv("SPLUNKTOKEN", "")
	t.Setenv("SPLUNKAPP", "")
	var output bytes.Buffer
	if err := validateSearchConfig(path, querypkg.Overrides{}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "valid: true") || !strings.Contains(output.String(), "mode: job") {
		t.Fatalf("unexpected validation output:\n%s", output.String())
	}
}

func TestValidateSearchConfigUsesEffectiveAppPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.yml")
	if err := os.WriteFile(path, []byte("search: search index=_internal earliest=-15m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPLUNKAPP", "environment-app")
	var environmentPlan bytes.Buffer
	if err := validateSearchConfig(path, querypkg.Overrides{}, &environmentPlan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(environmentPlan.String(), "app: environment-app") {
		t.Fatalf("SPLUNKAPP missing from effective plan:\n%s", environmentPlan.String())
	}

	explicitApp := "flag-app"
	var explicitPlan bytes.Buffer
	if err := validateSearchConfig(path, querypkg.Overrides{App: &explicitApp}, &explicitPlan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(explicitPlan.String(), "app: flag-app") || strings.Contains(explicitPlan.String(), "environment-app") {
		t.Fatalf("explicit app did not take precedence:\n%s", explicitPlan.String())
	}
}

func TestValidateSearchConfigPrintsBlockingPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.yml")
	if err := os.WriteFile(path, []byte("search: search index=* earliest=-15m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := validateSearchConfig(path, querypkg.Overrides{}, &output)
	if !errors.Is(err, querypkg.ErrSafetyViolation) || !strings.Contains(output.String(), "valid: false") || !strings.Contains(output.String(), "severity: violation") {
		t.Fatalf("error=%v output=\n%s", err, output.String())
	}
}

func TestRunConfigValidationSeparatesPlanAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.yml")
	if err := os.WriteFile(path, []byte("search: search index=* earliest=-15m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output, errorOutput bytes.Buffer
	if status := runConfigValidation(path, querypkg.Overrides{}, &output, &errorOutput); status != 1 {
		t.Fatalf("status = %d; want 1", status)
	}
	if !strings.Contains(output.String(), "valid: false") || strings.Contains(output.String(), "ERROR:") {
		t.Fatalf("standard output is not a clean plan:\n%s", output.String())
	}
	if !strings.Contains(errorOutput.String(), "ERROR: query safety violation") {
		t.Fatalf("standard error missing validation failure: %q", errorOutput.String())
	}
}

func TestValidateConfigModes(t *testing.T) {
	valid := [][3]string{
		{},
		{"search.yml", "", ""},
		{"", "search.yml", ""},
		{"", "", "search.yml"},
	}
	for _, modes := range valid {
		if err := validateConfigModes(modes[0], modes[1], modes[2]); err != nil {
			t.Fatalf("validateConfigModes%q returned %v", modes, err)
		}
	}

	conflicts := [][3]string{
		{"run.yml", "validate.yml", ""},
		{"", "validate.yml", "write.yml"},
		{"run.yml", "", "write.yml"},
		{"run.yml", "validate.yml", "write.yml"},
	}
	for _, modes := range conflicts {
		if err := validateConfigModes(modes[0], modes[1], modes[2]); err == nil {
			t.Fatalf("validateConfigModes%q accepted conflicting modes", modes)
		}
	}
}

func TestVersionStringDevelopmentDefaults(t *testing.T) {
	if got, want := versionString(), "querysplunk version=dev commit=unknown"; got != want {
		t.Fatalf("versionString() = %q; want %q", got, want)
	}
}

func TestVersionStringInjectedValues(t *testing.T) {
	originalVersion, originalCommit := version, commit
	version, commit = "v2.1.0", "0123456789ab"
	t.Cleanup(func() {
		version, commit = originalVersion, originalCommit
	})

	if got, want := versionString(), "querysplunk version=v2.1.0 commit=0123456789ab"; got != want {
		t.Fatalf("versionString() = %q; want %q", got, want)
	}
}

func TestTimeoutFromEnv(t *testing.T) {
	t.Setenv("SPLUNKTIMEOUT", "")
	val, err := timeoutFromEnv()
	if err != nil {
		t.Fatalf("expected default timeout, got error: %v", err)
	}
	if val != 120*time.Second {
		t.Fatalf("expected default timeout 120s, got %v", val)
	}

	cases := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "valid", value: "30", want: 30 * time.Second},
		{name: "negative", value: "-30", wantErr: true},
		{name: "invalid", value: "abc", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "whitespace", value: "  45  ", want: 45 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPLUNKTIMEOUT", tc.value)
			got, err := timeoutFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for value %q: %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("value %q expected %v, got %v", tc.value, tc.want, got)
			}
		})
	}
}

func TestTLSVerifyFromEnv(t *testing.T) {
	t.Setenv("SPLUNKTLSVERIFY", "")
	val, err := tlsVerifyFromEnv()
	if err != nil {
		t.Fatalf("expected default tls verify true, got error: %v", err)
	}
	if !val {
		t.Fatalf("expected default tls verify true, got false")
	}

	cases := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "1", value: "1", want: true},
		{name: "0", value: "0", want: false},
		{name: "invalid", value: "not-bool", wantErr: true},
		{name: "whitespace", value: "  false  ", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPLUNKTLSVERIFY", tc.value)
			got, err := tlsVerifyFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for value %q: %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("value %q expected %v, got %v", tc.value, tc.want, got)
			}
		})
	}
}

func TestReadSearchFile(t *testing.T) {
	queryFile := t.TempDir() + "/search.spl"
	want := "search index=_internal | head 1\n| table _time host\n"
	if err := os.WriteFile(queryFile, []byte(want), 0644); err != nil {
		t.Fatalf("write query fixture: %v", err)
	}

	got, err := readSearchFile(queryFile)
	if err != nil {
		t.Fatalf("read search file: %v", err)
	}
	if got != want {
		t.Fatalf("expected query contents %q, got %q", want, got)
	}
}

func TestDefaultQueryFileIsReadable(t *testing.T) {
	got, err := readSearchFile("query.txt")
	if err != nil {
		t.Fatalf("read default query file: %v", err)
	}
	if got == "" {
		t.Fatal("expected default query file to be non-empty")
	}
}

func TestDerivedSearchLogFile(t *testing.T) {
	tests := map[string]string{
		"splunkresults.json": "splunkresults.search.log",
		"splunkresults":      "splunkresults.search.log",
		"":                   "splunk.search.log",
	}
	for input, expected := range tests {
		if actual := querypkg.DerivedSearchLogFile(input); actual != expected {
			t.Errorf("derivedSearchLogFile(%q) = %q; want %q", input, actual, expected)
		}
	}
}

func TestRunSearchToFileReplacesOnlyAfterSuccess(t *testing.T) {
	const sid = "cross-platform-output"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/services/authentication/current-context":
			_, _ = w.Write([]byte(`{"entry":[{}]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/services/search/jobs/":
			if err := request.ParseForm(); err != nil {
				t.Fatalf("parse dispatch form: %v", err)
			}
			if request.FormValue("search") == "fail" {
				http.Error(w, "synthetic dispatch failure", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`<response><sid>` + sid + `</sid></response>`))
		case request.URL.Path == "/services/search/jobs/"+sid:
			_, _ = w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
		case request.URL.Path == "/services/search/v2/jobs/"+sid+"/results":
			_, _ = w.Write([]byte(`{"results":[{"status":"current"}]}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := splunk.NewClient(splunk.Config{
		BaseURL:      server.URL,
		Token:        "synthetic-token",
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

	output := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(output, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := querypkg.Prepare(querypkg.Config{Search: "success", OutputFile: output}, querypkg.Overrides{}, querypkg.UnsafeAllowAll())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.SearchToFile(context.Background(), client); err != nil {
		t.Fatalf("successful replacement: %v", err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(actual), `"status":"current"`) {
		t.Fatalf("result was not replaced: %q", actual)
	}

	const lastGood = `{"results":[{"status":"last-good"}]}`
	if err := os.WriteFile(output, []byte(lastGood), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err = querypkg.Prepare(querypkg.Config{Search: "fail", OutputFile: output}, querypkg.Overrides{}, querypkg.UnsafeAllowAll())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.SearchToFile(context.Background(), client); err == nil {
		t.Fatal("expected dispatch failure")
	}
	actual, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != lastGood {
		t.Fatalf("failed search changed last good result: %q", actual)
	}
}

func TestReadSearchFileRejectsEmptySearch(t *testing.T) {
	queryFile := t.TempDir() + "/empty.spl"
	if err := os.WriteFile(queryFile, []byte(" \n\t"), 0644); err != nil {
		t.Fatalf("write query fixture: %v", err)
	}

	_, err := readSearchFile(queryFile)
	if err == nil {
		t.Fatal("expected empty search file error")
	}
}

func TestReadSearchFileReturnsMissingFileError(t *testing.T) {
	_, err := readSearchFile(t.TempDir() + "/missing.spl")
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestWriteSkeletonConfig(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	if err := writeSkeletonConfig(configFile, false); err != nil {
		t.Fatalf("write skeleton config: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read skeleton config: %v", err)
	}
	if string(data) != skeletonSearchConfig {
		t.Fatalf("unexpected skeleton config contents: %q", string(data))
	}

	config, err := loadSearchConfig(configFile)
	if err != nil {
		t.Fatalf("skeleton config should parse: %v", err)
	}
	if strings.TrimSpace(config.Search) == "" {
		t.Fatal("expected skeleton config search content")
	}
}

func TestWriteSkeletonConfigRefusesOverwrite(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	if err := os.WriteFile(configFile, []byte("existing"), 0644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	err := writeSkeletonConfig(configFile, false)
	if err == nil {
		t.Fatal("expected overwrite error")
	}
}

func TestWriteSkeletonConfigForceOverwrites(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	if err := os.WriteFile(configFile, []byte("existing"), 0644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if err := writeSkeletonConfig(configFile, true); err != nil {
		t.Fatalf("force write skeleton config: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read skeleton config: %v", err)
	}
	if string(data) != skeletonSearchConfig {
		t.Fatalf("expected overwritten skeleton config, got %q", string(data))
	}
}

func TestLoadSearchConfig(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	content := `app: search
output_file: out.json
mode: job
search: |
  search index=_internal earliest=-15m
  | head 1
dispatch:
  earliest_time: "-15m"
  latest_time: "now"
  max_count: 50000
  status_buckets: 0
  required_fields:
    - sourcetype
results:
  endpoint: auto
  output_mode: json
  count: 0
  offset: 10
safety:
  allow_old_earliest: true
  allow_index_wildcard: true
diagnostics:
  search_log: both
  search_log_file: search.log
`
	if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	config, err := loadSearchConfig(configFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.App != "search" {
		t.Fatalf("expected app, got %q", config.App)
	}
	if config.OutputFile != "out.json" {
		t.Fatalf("expected output file, got %q", config.OutputFile)
	}
	if config.Mode != "job" {
		t.Fatalf("expected mode job, got %q", config.Mode)
	}
	if !strings.Contains(config.Search, "index=_internal") {
		t.Fatalf("expected search content, got %q", config.Search)
	}
	if config.Dispatch.MaxCount == nil || *config.Dispatch.MaxCount != 50000 {
		t.Fatalf("expected max_count 50000, got %#v", config.Dispatch.MaxCount)
	}
	if config.Results.Count == nil || *config.Results.Count != 0 {
		t.Fatalf("expected count 0, got %#v", config.Results.Count)
	}
	if config.Results.Endpoint != "auto" {
		t.Fatalf("expected results endpoint auto, got %q", config.Results.Endpoint)
	}
	if !config.Safety.AllowOldEarliest {
		t.Fatal("expected safety allow_old_earliest true")
	}
	if !config.Safety.AllowIndexWildcard {
		t.Fatal("expected safety allow_index_wildcard true")
	}
	if config.Diagnostics.SearchLog != "both" {
		t.Fatalf("expected diagnostics search_log both, got %q", config.Diagnostics.SearchLog)
	}
}

func TestLoadSearchConfigRejectsMissingSearch(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	if err := os.WriteFile(configFile, []byte("app: search\n"), 0644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := loadSearchConfig(configFile)
	if err == nil {
		t.Fatal("expected missing search error")
	}
}

func TestLoadSearchConfigRejectsInvalidSearchLogMode(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	content := `search: search index=_internal | head 1
diagnostics:
  search_log: loud
`
	if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := loadSearchConfig(configFile)
	if err == nil {
		t.Fatal("expected invalid search log mode error")
	}
}

func TestLoadSearchConfigRejectsInvalidResultEndpoint(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	content := `search: search index=_internal earliest=-15m | head 1
results:
  endpoint: legacy
`
	if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := loadSearchConfig(configFile)
	if err == nil {
		t.Fatal("expected invalid result endpoint error")
	}
}

func TestLoadSearchConfigRejectsInvalidExecutionMode(t *testing.T) {
	configFile := t.TempDir() + "/search.yml"
	content := `mode: realtime
search: search index=_internal earliest=-15m | head 1
`
	if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	_, err := loadSearchConfig(configFile)
	if err == nil {
		t.Fatal("expected invalid execution mode error")
	}
}

func TestHealthExampleConfigsLoad(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("examples", "health", "*.yml"))
	if err != nil {
		t.Fatalf("glob health examples: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected health example YAML files")
	}

	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			config, err := loadSearchConfig(path)
			if err != nil {
				t.Fatalf("load health example config: %v", err)
			}
			if strings.TrimSpace(config.OutputFile) == "" {
				t.Fatal("expected output_file")
			}
			if strings.TrimSpace(config.Mode) != "job" {
				t.Fatalf("expected mode job, got %q", config.Mode)
			}
			if strings.TrimSpace(config.Results.OutputMode) != "json" {
				t.Fatalf("expected results.output_mode json, got %q", config.Results.OutputMode)
			}
		})
	}
}

func TestConfigParams(t *testing.T) {
	maxCount := 50000
	statusBuckets := 0
	dispatch := dispatchParams(dispatchConfig{
		EarliestTime:   "-15m",
		LatestTime:     "now",
		MaxCount:       &maxCount,
		StatusBuckets:  &statusBuckets,
		RequiredFields: []string{"sourcetype", "host"},
	})
	if got := dispatch["earliest_time"][0]; got != "-15m" {
		t.Fatalf("expected earliest_time, got %q", got)
	}
	if got := dispatch["status_buckets"][0]; got != "0" {
		t.Fatalf("expected status_buckets 0, got %q", got)
	}
	if got := len(dispatch["rf"]); got != 2 {
		t.Fatalf("expected two required fields, got %d", got)
	}

	count := 0
	offset := 10
	results := resultParams(resultsConfig{OutputMode: "json", Count: &count, Offset: &offset})
	if got := results["count"][0]; got != "0" {
		t.Fatalf("expected count 0, got %q", got)
	}
	if got := results["offset"][0]; got != "10" {
		t.Fatalf("expected offset 10, got %q", got)
	}
}

func TestSetStringParam(t *testing.T) {
	params := map[string][]string{"earliest_time": {"-24h"}}
	if err := setStringParam(params, "earliest_time", " -15m "); err != nil {
		t.Fatalf("set string param: %v", err)
	}
	if got := params["earliest_time"]; len(got) != 1 || got[0] != "-15m" {
		t.Fatalf("expected override to single trimmed value, got %#v", got)
	}

	if err := setStringParam(params, "latest_time", " "); err == nil {
		t.Fatal("expected empty value error")
	}
}

func TestHasSearchTimeBounds(t *testing.T) {
	cases := []struct {
		name     string
		search   string
		dispatch map[string][]string
		want     bool
	}{
		{name: "spl earliest", search: "search index=_internal earliest=-15m | head 1", want: true},
		{name: "spl latest", search: "search index=_internal latest=now | head 1", want: true},
		{name: "dispatch earliest", search: "search index=_internal | head 1", dispatch: map[string][]string{"earliest_time": {"-15m"}}, want: true},
		{name: "dispatch latest", search: "search index=_internal | head 1", dispatch: map[string][]string{"latest_time": {"now"}}, want: true},
		{name: "unbounded", search: "search index=_internal | head 1", dispatch: map[string][]string{}, want: false},
		{name: "empty dispatch value", search: "search index=_internal | head 1", dispatch: map[string][]string{"earliest_time": {" "}}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasSearchTimeBounds(tc.search, tc.dispatch)
			if got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestSafetyViolationsBlocksOldEarliest(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		search   string
		dispatch map[string][]string
		want     bool
	}{
		{name: "spl older than one year", search: "search index=_internal earliest=-13mon | head 1", want: true},
		{name: "dispatch older than one year", search: "search index=_internal | head 1", dispatch: map[string][]string{"earliest_time": {"-2y"}}, want: true},
		{name: "absolute older than one year", search: "search index=_internal earliest=2025-07-09 | head 1", want: true},
		{name: "within one year", search: "search index=_internal earliest=-12mon | head 1", want: false},
		{name: "quoted within one year", search: `search index=_internal earliest="-30d" | head 1`, want: false},
		{name: "unknown earliest syntax", search: "search index=_internal earliest=@d | head 1", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := safetyViolations(tc.search, tc.dispatch, now, false, false)
			hasViolation := len(got) > 0
			if hasViolation != tc.want {
				t.Fatalf("expected violation=%v, got %v (%#v)", tc.want, hasViolation, got)
			}
		})
	}
}

func TestSafetyViolationsBlocksIndexWildcard(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		search string
		want   bool
	}{
		{name: "bare wildcard", search: "search index=* earliest=-15m | head 1", want: true},
		{name: "quoted wildcard", search: `search index="*" earliest=-15m | head 1`, want: true},
		{name: "non-wildcard index", search: "search index=_internal earliest=-15m | head 1", want: false},
		{name: "wildcard suffix is allowed", search: "search index=main* earliest=-15m | head 1", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := safetyViolations(tc.search, nil, now, false, false)
			hasViolation := len(got) > 0
			if hasViolation != tc.want {
				t.Fatalf("expected violation=%v, got %v (%#v)", tc.want, hasViolation, got)
			}
		})
	}
}

func TestSafetyViolationOverrides(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	search := "search index=* earliest=-2y | head 1"
	if got := safetyViolations(search, nil, now, false, false); len(got) != 2 {
		t.Fatalf("expected two safety violations, got %#v", got)
	}
	if got := safetyViolations(search, nil, now, true, false); len(got) != 1 || !strings.Contains(got[0], "index=*") {
		t.Fatalf("expected only index wildcard violation, got %#v", got)
	}
	if got := safetyViolations(search, nil, now, true, true); len(got) != 0 {
		t.Fatalf("expected overrides to allow search, got %#v", got)
	}
}

func TestSafetyConfigOverrides(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	config := searchConfig{
		Search: "search index=* earliest=-2y | head 1",
		Safety: safetyConfig{
			AllowOldEarliest:   true,
			AllowIndexWildcard: true,
		},
	}

	got := safetyViolations(config.Search, nil, now, config.Safety.AllowOldEarliest, config.Safety.AllowIndexWildcard)
	if len(got) != 0 {
		t.Fatalf("expected YAML safety config to allow search, got %#v", got)
	}
}
