package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

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
