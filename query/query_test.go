package query_test

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
	"testing"
	"testing/fstest"
	"time"

	"github.com/georgestarcher/querysplunk/v2/query"
	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestLoadStrictSchemaAndAllFields(t *testing.T) {
	config, err := query.Load(strings.NewReader(query.SkeletonConfig))
	if err != nil {
		t.Fatal(err)
	}
	options, err := config.SearchOptions()
	if err != nil {
		t.Fatal(err)
	}
	if config.SchemaVersion != query.CurrentSchemaVersion || config.App != "search" || config.OutputFile != "splunkresults.json" || config.Mode != "job" {
		t.Fatalf("top-level fields not decoded: %#v", config)
	}
	if got := options.DispatchParams["max_count"]; len(got) != 1 || got[0] != "50000" {
		t.Fatalf("dispatch fields not converted: %#v", options.DispatchParams)
	}
	if got := options.ResultParams["count"]; len(got) != 1 || got[0] != "0" {
		t.Fatalf("result fields not converted: %#v", options.ResultParams)
	}
	if options.ResultEndpoint != splunk.ResultEndpointAuto || options.SearchLog != splunk.SearchLogModeSummary {
		t.Fatalf("modes not converted: %#v", options)
	}
}

func TestLoadRejectsInvalidInput(t *testing.T) {
	tests := map[string]string{
		"unknown field":       "search: search earliest=-5m\nunknown: true\n",
		"duplicate key":       "search: one\nsearch: two\n",
		"malformed":           "search: [\n",
		"missing search":      "app: search\n",
		"invalid mode":        "search: search earliest=-5m\nmode: realtime\n",
		"invalid endpoint":    "search: search earliest=-5m\nresults:\n  endpoint: old\n",
		"invalid diagnostics": "search: search earliest=-5m\ndiagnostics:\n  search_log: loud\n",
		"incompatible log":    "search: search earliest=-5m\ndiagnostics:\n  search_log: summary\n  search_log_file: raw.log\n",
		"negative count":      "search: search earliest=-5m\nresults:\n  count: -1\n",
		"multiple documents":  "search: search earliest=-5m\n---\nsearch: second\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := query.Load(strings.NewReader(input))
			if !errors.Is(err, query.ErrInvalidConfig) {
				t.Fatalf("error = %v; want ErrInvalidConfig", err)
			}
		})
	}
}

func TestLoadFSAndHealthFixtures(t *testing.T) {
	files := fstest.MapFS{"saved.yml": &fstest.MapFile{Data: []byte("search: search index=_internal earliest=-5m\n")}}
	config, err := query.LoadFS(files, "saved.yml")
	if err != nil || !strings.Contains(config.Search, "_internal") {
		t.Fatalf("LoadFS = %#v, %v", config, err)
	}
	matches, err := filepath.Glob(filepath.Join("..", "examples", "health", "*.yml"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("health fixtures: %v, %v", matches, err)
	}
	for _, path := range matches {
		relative, err := filepath.Rel("..", path)
		if err != nil {
			t.Errorf("make %s relative: %v", path, err)
			continue
		}
		relative = filepath.ToSlash(relative)
		if _, err := query.LoadFS(os.DirFS(".."), relative); err != nil {
			t.Errorf("load %s: %v", path, err)
		}
	}
}

func TestPrepareMergePrecedenceAndCopies(t *testing.T) {
	app, output, earliest, latest := "override", "override.json", "-30m", "now"
	config := query.Config{Search: "search index=_internal earliest=-2y", App: "yaml", OutputFile: "yaml.json", Dispatch: query.Dispatch{RequiredFields: []string{"host"}}}
	prepared, err := query.Prepare(config, query.Overrides{App: &app, OutputFile: &output, EarliestTime: &earliest, LatestTime: &latest, AllowOldEarliest: true}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	config.Dispatch.RequiredFields[0] = "mutated"
	got := prepared.Config()
	if got.App != app || got.OutputFile != output || got.Dispatch.EarliestTime != earliest || got.Dispatch.LatestTime != latest || got.Dispatch.RequiredFields[0] != "host" {
		t.Fatalf("unexpected merged copy: %#v", got)
	}
	if options := prepared.Options(); options.SearchLog != splunk.SearchLogModeSummary {
		t.Fatalf("default search.log mode = %q; want summary", options.SearchLog)
	}
}

func TestSafetyFindingsAndAcknowledgements(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	config := query.Config{Search: "search index=* earliest=-2y | head 1"}
	prepared, err := query.Prepare(config, query.Overrides{}, query.SafetyPolicy{Now: func() time.Time { return now }})
	if !errors.Is(err, query.ErrSafetyViolation) {
		t.Fatalf("error = %v; want safety violation", err)
	}
	var violation *query.ViolationError
	if !errors.As(err, &violation) || len(violation.Findings) != 2 || len(prepared.Findings()) != 2 {
		t.Fatalf("typed findings not preserved: prepared=%#v error=%#v", prepared.Findings(), err)
	}
	prepared, err = query.Prepare(config, query.Overrides{AllowOldEarliest: true, AllowIndexWildcard: true}, query.SafetyPolicy{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range prepared.Findings() {
		if finding.Severity != query.SeverityAcknowledged {
			t.Fatalf("finding not acknowledged: %#v", finding)
		}
	}
	prepared, err = query.Prepare(query.Config{Search: "| makeresults"}, query.Overrides{}, query.UnsafeAllowAll())
	if err != nil || len(prepared.Findings()) != 1 || prepared.Findings()[0].Severity != query.SeverityWarning {
		t.Fatalf("unsafe bypass lost warning: %#v, %v", prepared.Findings(), err)
	}
}

func TestPlanUsesEffectiveConfigAndDeterministicYAML(t *testing.T) {
	app, output, earliest, latest := "security", "override.json", "-30m", "now"
	prepared, err := query.Prepare(
		query.Config{Search: "search index=* earliest=-2y", Diagnostics: query.Diagnostics{SearchLog: "save"}},
		query.Overrides{App: &app, OutputFile: &output, EarliestTime: &earliest, LatestTime: &latest, AllowOldEarliest: true, AllowIndexWildcard: true},
		query.SafetyPolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := prepared.Plan()
	if !plan.Valid || plan.Config.App != app || plan.Config.OutputFile != output || plan.Config.Mode != "job" || plan.Config.Results.Endpoint != "auto" {
		t.Fatalf("unexpected effective plan: %#v", plan)
	}
	if plan.Config.Dispatch.EarliestTime != earliest || plan.Config.Dispatch.LatestTime != latest || plan.Config.Diagnostics.SearchLogFile != "override.search.log" {
		t.Fatalf("derived settings missing from plan: %#v", plan.Config)
	}
	if !plan.Config.Safety.AllowOldEarliest || !plan.Config.Safety.AllowIndexWildcard || len(plan.Findings) != 2 {
		t.Fatalf("safety acknowledgements missing from plan: %#v", plan)
	}
	for _, finding := range plan.Findings {
		if finding.Severity != query.SeverityAcknowledged {
			t.Fatalf("finding was not acknowledged: %#v", finding)
		}
	}

	var first, second bytes.Buffer
	if err := plan.EncodeYAML(&first); err != nil {
		t.Fatal(err)
	}
	if err := plan.EncodeYAML(&second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() || !strings.Contains(first.String(), "valid: true") {
		t.Fatalf("plan YAML is not deterministic:\n%s\n%s", first.String(), second.String())
	}
}

func TestPlanPreservesBlockingFindings(t *testing.T) {
	prepared, err := query.Prepare(query.Config{Search: "search index=* earliest=-5m"}, query.Overrides{}, query.SafetyPolicy{})
	if !errors.Is(err, query.ErrSafetyViolation) {
		t.Fatalf("error = %v; want safety violation", err)
	}
	plan := prepared.Plan()
	if plan.Valid || len(plan.Findings) != 1 || plan.Findings[0].Severity != query.SeverityViolation {
		t.Fatalf("unexpected blocking plan: %#v", plan)
	}
	if err := plan.EncodeYAML(nil); !errors.Is(err, query.ErrInvalidConfig) {
		t.Fatalf("nil writer error = %v; want ErrInvalidConfig", err)
	}
	if err := plan.EncodeYAML(failingWriter{}); err == nil {
		t.Fatal("expected plan writer error")
	}
	if (query.Prepared{}).Plan().Valid {
		t.Fatal("zero-value Prepared must not produce a valid plan")
	}
	warningOnly, err := query.Prepare(query.Config{Search: "| makeresults"}, query.Overrides{}, query.SafetyPolicy{})
	if err != nil || !warningOnly.Plan().Valid || len(warningOnly.Plan().Findings) != 1 || warningOnly.Plan().Findings[0].Severity != query.SeverityWarning {
		t.Fatalf("warning-only plan = %#v, error = %v", warningOnly.Plan(), err)
	}
}

func TestPreparedExecutionStreamingDiagnosticsAndAtomicReplacement(t *testing.T) {
	server := queryServer(t)
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client(), PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Splunk client: %v", err)
		}
	})
	output := filepath.Join(t.TempDir(), "result.json")
	prepared, err := query.Prepare(query.Config{Search: "success earliest=-5m", OutputFile: output, Diagnostics: query.Diagnostics{SearchLog: "summary"}}, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.SearchToFile(context.Background(), client)
	if err != nil || !result.SearchLogRead || len(result.Diagnostics.Warnings) != 1 {
		t.Fatalf("SearchToFile result=%#v error=%v", result, err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(data, []byte(`"current"`)) {
		t.Fatalf("result file=%q error=%v", data, err)
	}
	lastGood := []byte(`{"last":"good"}`)
	if err := os.WriteFile(output, lastGood, 0600); err != nil {
		t.Fatal(err)
	}
	failed, err := query.Prepare(query.Config{Search: "fail earliest=-5m", OutputFile: output}, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failed.SearchToFile(context.Background(), client); err == nil {
		t.Fatal("expected dispatch failure")
	}
	data, _ = os.ReadFile(output)
	if !bytes.Equal(data, lastGood) {
		t.Fatalf("failed search replaced last-good output: %q", data)
	}
	var streamed strings.Builder
	if _, err := prepared.SearchTo(context.Background(), client, &streamed); err != nil || streamed.Len() == 0 {
		t.Fatalf("streamed=%d error=%v", streamed.Len(), err)
	}
	if _, err := prepared.SearchTo(context.Background(), client, failingWriter{}); err == nil {
		t.Fatal("expected writer failure")
	}
}

func TestPreparedPreservesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := query.Prepare(query.Config{Search: "search earliest=-5m"}, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = prepared.Search(ctx, client)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v; want context.Canceled", err)
	}
}

func TestWriteSkeleton(t *testing.T) {
	path := filepath.Join(t.TempDir(), "saved.yml")
	if err := query.WriteSkeleton(path, false); err != nil {
		t.Fatal(err)
	}
	if err := query.WriteSkeleton(path, false); err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if err := query.WriteSkeleton(path, true); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaVersionCompatibility(t *testing.T) {
	legacy, err := query.Load(strings.NewReader("search: search index=_internal earliest=-5m\n"))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != "" {
		t.Fatalf("legacy load schema version = %q; want omitted", legacy.SchemaVersion)
	}
	prepared, err := query.Prepare(legacy, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Plan().Config.SchemaVersion != query.CurrentSchemaVersion {
		t.Fatalf("effective schema version = %q; want %q", prepared.Plan().Config.SchemaVersion, query.CurrentSchemaVersion)
	}

	_, err = query.Load(strings.NewReader("schema_version: \"2\"\nsearch: search index=_internal earliest=-5m\n"))
	if !errors.Is(err, query.ErrInvalidConfig) || !errors.Is(err, query.ErrUnsupportedSchemaVersion) {
		t.Fatalf("unsupported schema error = %v", err)
	}
	var versionError *query.SchemaVersionError
	if !errors.As(err, &versionError) || versionError.Version != "2" {
		t.Fatalf("typed schema error = %#v", versionError)
	}
}

func TestMetadataValidationAndCopyIsolation(t *testing.T) {
	config := query.Config{
		SchemaVersion: query.CurrentSchemaVersion,
		Search:        "search index=_internal earliest=-5m",
		Metadata: &query.Metadata{
			ID: "querysplunk.health.example", Title: "Example", Description: "Example search.", Category: "health",
			Status: "stable", Version: 1, Author: "querysplunk contributors", Created: "2026-07-15", Severity: "low", Tags: []string{"example"},
		},
		Requirements:   &query.Requirements{Platforms: []string{"splunk-cloud"}, Apps: []string{"search"}, Indexes: []string{"_internal"}, Fields: []string{"host"}},
		Provenance:     &query.Provenance{Source: "querysplunk", SourceURL: "https://github.com/georgestarcher/querysplunk", SourceRuleIDs: []string{"local-example"}, License: "MIT"},
		Interpretation: &query.Interpretation{Summary: "Example interpretation.", FalsePositives: []string{"Test data."}, RecommendedActions: []string{"Review the event."}},
	}
	prepared, err := query.Prepare(config, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	config.Metadata.Tags[0] = "mutated"
	config.Requirements.Platforms[0] = "mutated"
	config.Provenance.SourceRuleIDs[0] = "mutated"
	config.Interpretation.FalsePositives[0] = "mutated"
	got := prepared.Config()
	if got.Metadata.Tags[0] != "example" || got.Requirements.Platforms[0] != "splunk-cloud" || got.Provenance.SourceRuleIDs[0] != "local-example" || got.Interpretation.FalsePositives[0] != "Test data." {
		t.Fatalf("prepared config aliases caller data: %#v", got)
	}
	got.Metadata.Tags[0] = "returned mutation"
	if prepared.Config().Metadata.Tags[0] != "example" {
		t.Fatal("prepared config accessor returned aliased metadata")
	}

	tests := map[string]query.Config{
		"partial metadata": {Search: config.Search, Metadata: &query.Metadata{ID: "querysplunk.partial"}},
		"invalid id":       {Search: config.Search, Metadata: &query.Metadata{ID: "Query Splunk", Title: "Example", Description: "Example.", Category: "health", Status: "stable", Version: 1, Author: "querysplunk"}},
		"invalid status":   {Search: config.Search, Metadata: &query.Metadata{ID: "querysplunk.example", Title: "Example", Description: "Example.", Category: "health", Status: "ready", Version: 1, Author: "querysplunk"}},
		"invalid severity": {Search: config.Search, Metadata: &query.Metadata{ID: "querysplunk.example", Title: "Example", Description: "Example.", Category: "health", Status: "stable", Version: 1, Author: "querysplunk", Severity: "urgent"}},
		"invalid date":     {Search: config.Search, Metadata: &query.Metadata{ID: "querysplunk.example", Title: "Example", Description: "Example.", Category: "health", Status: "stable", Version: 1, Author: "querysplunk", Created: "07/15/2026"}},
		"duplicate tag":    {Search: config.Search, Metadata: &query.Metadata{ID: "querysplunk.example", Title: "Example", Description: "Example.", Category: "health", Status: "stable", Version: 1, Author: "querysplunk", Tags: []string{"same", "same"}}},
		"invalid source":   {Search: config.Search, Provenance: &query.Provenance{Source: "external", SourceURL: "relative/path", License: "MIT"}},
		"empty summary":    {Search: config.Search, Interpretation: &query.Interpretation{}},
	}
	for name, invalid := range tests {
		t.Run(name, func(t *testing.T) {
			if err := invalid.Validate(); !errors.Is(err, query.ErrInvalidConfig) {
				t.Fatalf("error = %v; want ErrInvalidConfig", err)
			}
		})
	}
}

func TestBundledSearchesHaveUniqueCompleteMetadata(t *testing.T) {
	patterns := []string{"../examples/*/*.yml", "../.agents/skills/querysplunk/templates/*.yml"}
	seen := make(map[string]string)
	count := 0
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range matches {
			if filepath.Base(path) == "handoff.yml" {
				continue
			}
			config, err := query.LoadFile(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			count++
			if config.SchemaVersion != query.CurrentSchemaVersion || config.Metadata == nil || config.Requirements == nil || config.Provenance == nil || config.Interpretation == nil {
				t.Errorf("%s is missing complete OOB schema blocks", path)
				continue
			}
			if previous, ok := seen[config.Metadata.ID]; ok {
				t.Errorf("metadata id %q is duplicated by %s and %s", config.Metadata.ID, previous, path)
			}
			seen[config.Metadata.ID] = path
		}
	}
	if count == 0 {
		t.Fatal("no bundled search YAML files found")
	}
}

func FuzzLoad(f *testing.F) {
	f.Add([]byte(query.SkeletonConfig))
	f.Add([]byte("search: search earliest=-5m\n"))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = query.Load(bytes.NewReader(data)) })
}

func FuzzSafetyAnalysis(f *testing.F) {
	f.Add("search index=* earliest=-2y", "-2y")
	f.Add("search index=_internal earliest=-5m", "")
	f.Fuzz(func(t *testing.T, search, earliest string) {
		if len(search)+len(earliest) > 1<<16 {
			t.Skip()
		}
		_ = query.Analyze(query.Config{Search: search, Dispatch: query.Dispatch{EarliestTime: earliest}}, query.SafetyPolicy{})
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func queryServer(t *testing.T) *httptest.Server {
	t.Helper()
	const sid = "query-package"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = w.Write([]byte(`{"entry":[{}]}`))
		case "/services/search/jobs/":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if strings.Contains(request.FormValue("search"), "fail") {
				http.Error(w, "synthetic failure", http.StatusInternalServerError)
				return
			}
			_, _ = fmt.Fprintf(w, "<response><sid>%s</sid></response>", sid)
		case "/services/search/jobs/" + sid:
			_, _ = w.Write([]byte(`{"entry":[{"content":{"dispatchState":"DONE"}}]}`))
		case "/services/search/jobs/" + sid + "/search.log":
			_, _ = w.Write([]byte("WARN synthetic warning"))
		case "/services/search/v2/jobs/" + sid + "/results":
			_, _ = w.Write([]byte(`{"results":[{"status":"current"}]}`))
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
