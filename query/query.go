// Package query loads, validates, and executes querysplunk saved searches.
// It owns the public YAML schema and deployment-impact safety policy; package
// splunk remains responsible for authentication and REST transport.
package query

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
	"gopkg.in/yaml.v3"
)

const (
	maxYAMLBytes = 1 << 20
	// SkeletonConfig is the secret-free YAML written by WriteSkeleton.
	SkeletonConfig = `app: search
output_file: splunkresults.json
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
  offset: 0

safety:
  allow_old_earliest: false
  allow_index_wildcard: false

diagnostics:
  search_log: summary
  # search_log_file: splunkresults.search.log
`
)

var (
	ErrInvalidConfig        = errors.New("invalid query configuration")
	ErrSafetyViolation      = errors.New("query safety violation")
	timeModifierPattern     = regexp.MustCompile(`(?i)(^|\s)(earliest|latest)\s*=`)
	earliestPattern         = regexp.MustCompile(`(?i)(^|[\s(])earliest\s*=\s*("[^"]+"|'[^']+'|[^\s|,)]+)`)
	indexWildcardPattern    = regexp.MustCompile(`(?i)(^|[\s(])index\s*=\s*("[*]"|'[*]'|[*])($|[\s|,)])`)
	relativeEarliestPattern = regexp.MustCompile(`(?i)^-(\d+)(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks|mon|month|months|q|qtr|quarter|quarters|y|yr|yrs|year|years)(@[a-z0-9]+)?$`)
)

// Config is the querysplunk YAML schema. Credentials never belong here.
type Config struct {
	App         string      `json:"app" yaml:"app"`
	OutputFile  string      `json:"output_file" yaml:"output_file"`
	Mode        string      `json:"mode" yaml:"mode"`
	Search      string      `json:"search" yaml:"search"`
	Safety      Safety      `json:"safety" yaml:"safety"`
	Dispatch    Dispatch    `json:"dispatch" yaml:"dispatch"`
	Results     Results     `json:"results" yaml:"results"`
	Diagnostics Diagnostics `json:"diagnostics" yaml:"diagnostics"`
}

type Safety struct {
	AllowOldEarliest   bool `json:"allow_old_earliest" yaml:"allow_old_earliest"`
	AllowIndexWildcard bool `json:"allow_index_wildcard" yaml:"allow_index_wildcard"`
}

type Dispatch struct {
	EarliestTime   string   `json:"earliest_time" yaml:"earliest_time"`
	LatestTime     string   `json:"latest_time" yaml:"latest_time"`
	MaxCount       *int     `json:"max_count" yaml:"max_count"`
	StatusBuckets  *int     `json:"status_buckets" yaml:"status_buckets"`
	RequiredFields []string `json:"required_fields" yaml:"required_fields"`
}

type Results struct {
	OutputMode string `json:"output_mode" yaml:"output_mode"`
	Count      *int   `json:"count" yaml:"count"`
	Offset     *int   `json:"offset" yaml:"offset"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
}

type Diagnostics struct {
	SearchLog     string `json:"search_log" yaml:"search_log"`
	SearchLogFile string `json:"search_log_file" yaml:"search_log_file"`
}

// Overrides are applied after loading and before safety analysis. Nil pointers
// mean no override. Risk acknowledgements are intentionally one-way.
type Overrides struct {
	App                *string
	OutputFile         *string
	EarliestTime       *string
	LatestTime         *string
	AllowOldEarliest   bool
	AllowIndexWildcard bool
}

// SafetyPolicy has safe zero-value defaults: one calendar year and index=*
// blocking. Now is available for deterministic analysis and tests.
type SafetyPolicy struct {
	MaxEarliestAgeYears int
	AllowOldEarliest    bool
	AllowIndexWildcard  bool
	Now                 func() time.Time
	unsafeAllowAll      bool
}

// UnsafeAllowAll deliberately acknowledges all blocking findings. Missing
// time bounds remain visible as a warning. Do not use it for untrusted input.
func UnsafeAllowAll() SafetyPolicy { return SafetyPolicy{unsafeAllowAll: true} }

type FindingSeverity string

const (
	SeverityWarning         FindingSeverity = "warning"
	SeverityViolation       FindingSeverity = "violation"
	SeverityAcknowledged    FindingSeverity = "acknowledged"
	FindingMissingTimeBound                 = "missing_time_bound"
	FindingOldEarliest                      = "old_earliest"
	FindingIndexWildcard                    = "index_wildcard"
)

// Finding is a warning, blocking violation, or explicit acknowledgement.
type Finding struct {
	Kind     string          `json:"kind" yaml:"kind"`
	Severity FindingSeverity `json:"severity" yaml:"severity"`
	Message  string          `json:"message" yaml:"message"`
}

// Plan is the deterministic, credential-free description of a prepared query.
// Valid is false when Findings contains a blocking safety violation. Successful
// offline validation does not prove Splunk authorization or execution.
type Plan struct {
	Valid    bool      `json:"valid" yaml:"valid"`
	Config   Config    `json:"config" yaml:"config"`
	Findings []Finding `json:"findings" yaml:"findings"`
}

// ViolationError contains every blocking finding from one Prepare call.
type ViolationError struct{ Findings []Finding }

func (err *ViolationError) Error() string {
	messages := make([]string, 0, len(err.Findings))
	for _, finding := range err.Findings {
		messages = append(messages, finding.Message)
	}
	return fmt.Sprintf("%s: %s", ErrSafetyViolation, strings.Join(messages, "; "))
}
func (err *ViolationError) Unwrap() error { return ErrSafetyViolation }

// Prepared is an immutable validated query. Copies returned by its accessors
// prevent caller mutation; it may be reused concurrently with safe clients and
// independent output writers.
type Prepared struct {
	config   Config
	options  splunk.SearchOptions
	findings []Finding
}

// Load strictly decodes one YAML document. Input is bounded to 1 MiB; unknown
// fields, duplicate keys, malformed values, and incompatible settings fail.
func Load(reader io.Reader) (Config, error) {
	if reader == nil {
		return Config{}, fmt.Errorf("%w: reader is required", ErrInvalidConfig)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxYAMLBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("%w: read YAML: %v", ErrInvalidConfig, err)
	}
	if len(data) > maxYAMLBytes {
		return Config{}, fmt.Errorf("%w: YAML exceeds %d bytes", ErrInvalidConfig, maxYAMLBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%w: decode YAML: %v", ErrInvalidConfig, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents are not supported")
		}
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return cloneConfig(config), nil
}

func LoadFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer func() {
		_ = file.Close()
	}()
	config, err := Load(file)
	if err != nil {
		return Config{}, fmt.Errorf("load query config %q: %w", path, err)
	}
	return config, nil
}

// LoadFS supports embedded and bundled saved searches through fs.FS.
func LoadFS(files fs.FS, path string) (Config, error) {
	file, err := files.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer func() {
		_ = file.Close()
	}()
	config, err := Load(file)
	if err != nil {
		return Config{}, fmt.Errorf("load query config %q: %w", path, err)
	}
	return config, nil
}

// Validate checks schema semantics without applying deployment safety policy.
func (config Config) Validate() error {
	if strings.TrimSpace(config.Search) == "" {
		return fmt.Errorf("%w: search content is required", ErrInvalidConfig)
	}
	mode := splunk.ExecutionMode(strings.TrimSpace(config.Mode))
	if mode != "" && mode != splunk.ExecutionModeJob && mode != splunk.ExecutionModeExport {
		return fmt.Errorf("%w: mode %q must be job or export", ErrInvalidConfig, config.Mode)
	}
	endpoint := splunk.ResultEndpointMode(strings.TrimSpace(config.Results.Endpoint))
	if endpoint != "" && endpoint != splunk.ResultEndpointAuto && endpoint != splunk.ResultEndpointV1 && endpoint != splunk.ResultEndpointV2 {
		return fmt.Errorf("%w: results.endpoint %q must be auto, v1, or v2", ErrInvalidConfig, config.Results.Endpoint)
	}
	logMode := splunk.SearchLogMode(strings.TrimSpace(config.Diagnostics.SearchLog))
	if logMode != "" && logMode != splunk.SearchLogModeOff && logMode != splunk.SearchLogModeSummary && logMode != splunk.SearchLogModeSave && logMode != splunk.SearchLogModeBoth {
		return fmt.Errorf("%w: diagnostics.search_log %q must be off, summary, save, or both", ErrInvalidConfig, config.Diagnostics.SearchLog)
	}
	if mode == splunk.ExecutionModeExport && (logMode == splunk.SearchLogModeSave || logMode == splunk.SearchLogModeBoth || strings.TrimSpace(config.Diagnostics.SearchLogFile) != "") {
		return fmt.Errorf("%w: export mode cannot save search.log", ErrInvalidConfig)
	}
	if strings.TrimSpace(config.Diagnostics.SearchLogFile) != "" && logMode != splunk.SearchLogModeSave && logMode != splunk.SearchLogModeBoth {
		return fmt.Errorf("%w: diagnostics.search_log_file requires save or both mode", ErrInvalidConfig)
	}
	values := map[string]*int{"dispatch.max_count": config.Dispatch.MaxCount, "dispatch.status_buckets": config.Dispatch.StatusBuckets, "results.count": config.Results.Count, "results.offset": config.Results.Offset}
	for name, value := range values {
		if value != nil && *value < 0 {
			return fmt.Errorf("%w: %s cannot be negative", ErrInvalidConfig, name)
		}
	}
	return nil
}

// ApplyOverrides returns an independent config with overrides applied.
func ApplyOverrides(config Config, overrides Overrides) (Config, error) {
	merged := cloneConfig(config)
	if overrides.App != nil {
		merged.App = strings.TrimSpace(*overrides.App)
	}
	if overrides.OutputFile != nil {
		if strings.TrimSpace(*overrides.OutputFile) == "" {
			return Config{}, fmt.Errorf("%w: output file override cannot be empty", ErrInvalidConfig)
		}
		merged.OutputFile = strings.TrimSpace(*overrides.OutputFile)
	}
	if overrides.EarliestTime != nil {
		if strings.TrimSpace(*overrides.EarliestTime) == "" {
			return Config{}, fmt.Errorf("%w: earliest_time override cannot be empty", ErrInvalidConfig)
		}
		merged.Dispatch.EarliestTime = strings.TrimSpace(*overrides.EarliestTime)
	}
	if overrides.LatestTime != nil {
		if strings.TrimSpace(*overrides.LatestTime) == "" {
			return Config{}, fmt.Errorf("%w: latest_time override cannot be empty", ErrInvalidConfig)
		}
		merged.Dispatch.LatestTime = strings.TrimSpace(*overrides.LatestTime)
	}
	merged.Safety.AllowOldEarliest = merged.Safety.AllowOldEarliest || overrides.AllowOldEarliest
	merged.Safety.AllowIndexWildcard = merged.Safety.AllowIndexWildcard || overrides.AllowIndexWildcard
	return merged, nil
}

// Analyze reports warnings, violations, and acknowledgements without executing.
func Analyze(config Config, policy SafetyPolicy) []Finding {
	policy = normalizedPolicy(policy)
	now := time.Now()
	if policy.Now != nil {
		now = policy.Now()
	}
	options, _ := config.SearchOptions()
	findings := make([]Finding, 0, 3)
	if !HasTimeBounds(config.Search, options.DispatchParams) {
		findings = append(findings, Finding{Kind: FindingMissingTimeBound, Severity: SeverityWarning, Message: "no SPL or dispatch earliest/latest time bound was provided; Splunk REST searches can run over all time"})
	}
	allowWildcard := policy.unsafeAllowAll || policy.AllowIndexWildcard || config.Safety.AllowIndexWildcard
	if indexWildcardPattern.MatchString(config.Search) {
		severity, message := SeverityViolation, "search uses index=*, which can unexpectedly fan out across a Splunk deployment"
		if allowWildcard {
			severity, message = SeverityAcknowledged, message+"; risk explicitly acknowledged"
		}
		findings = append(findings, Finding{Kind: FindingIndexWildcard, Severity: severity, Message: message})
	}
	allowOld := policy.unsafeAllowAll || policy.AllowOldEarliest || config.Safety.AllowOldEarliest
	cutoff := now.AddDate(-policy.MaxEarliestAgeYears, 0, 0)
	for _, value := range earliestValues(config.Search, options.DispatchParams) {
		parsed, ok := parseEarliest(value, now)
		if !ok || !parsed.Before(cutoff) {
			continue
		}
		severity := SeverityViolation
		message := fmt.Sprintf("earliest=%s is older than the %d-year safety limit", value, policy.MaxEarliestAgeYears)
		if allowOld {
			severity, message = SeverityAcknowledged, message+"; risk explicitly acknowledged"
		}
		findings = append(findings, Finding{Kind: FindingOldEarliest, Severity: severity, Message: message})
	}
	return findings
}

// Prepare applies overrides, defaults, validation, conversion, and policy in
// that order. Blocking findings are returned as *ViolationError.
func Prepare(config Config, overrides Overrides, policy SafetyPolicy) (Prepared, error) {
	merged, err := ApplyOverrides(config, overrides)
	if err != nil {
		return Prepared{}, err
	}
	merged = defaults(merged)
	if err := merged.Validate(); err != nil {
		return Prepared{}, err
	}
	options, err := merged.SearchOptions()
	if err != nil {
		return Prepared{}, err
	}
	findings := Analyze(merged, policy)
	prepared := Prepared{config: cloneConfig(merged), options: cloneOptions(options), findings: append([]Finding(nil), findings...)}
	violations := findingsBySeverity(findings, SeverityViolation)
	if len(violations) != 0 {
		return prepared, &ViolationError{Findings: violations}
	}
	return prepared, nil
}

// SearchOptions converts the public schema without exposing private transport types.
func (config Config) SearchOptions() (splunk.SearchOptions, error) {
	if err := config.Validate(); err != nil {
		return splunk.SearchOptions{}, err
	}
	options := splunk.SearchOptions{DispatchParams: make(map[string][]string), ResultParams: make(map[string][]string), ResultEndpoint: splunk.ResultEndpointMode(strings.TrimSpace(config.Results.Endpoint)), ExecutionMode: splunk.ExecutionMode(strings.TrimSpace(config.Mode)), SearchLog: splunk.SearchLogMode(strings.TrimSpace(config.Diagnostics.SearchLog)), SearchLogFile: strings.TrimSpace(config.Diagnostics.SearchLogFile)}
	addString(options.DispatchParams, "earliest_time", config.Dispatch.EarliestTime)
	addString(options.DispatchParams, "latest_time", config.Dispatch.LatestTime)
	addInt(options.DispatchParams, "max_count", config.Dispatch.MaxCount)
	addInt(options.DispatchParams, "status_buckets", config.Dispatch.StatusBuckets)
	for _, field := range config.Dispatch.RequiredFields {
		addString(options.DispatchParams, "rf", field)
	}
	addString(options.ResultParams, "output_mode", config.Results.OutputMode)
	addInt(options.ResultParams, "count", config.Results.Count)
	addInt(options.ResultParams, "offset", config.Results.Offset)
	if (options.SearchLog == splunk.SearchLogModeSave || options.SearchLog == splunk.SearchLogModeBoth) && options.SearchLogFile == "" {
		options.SearchLogFile = DerivedSearchLogFile(config.OutputFile)
	}
	return options, nil
}

func (prepared Prepared) Config() Config                { return cloneConfig(prepared.config) }
func (prepared Prepared) Options() splunk.SearchOptions { return cloneOptions(prepared.options) }
func (prepared Prepared) Findings() []Finding           { return append([]Finding(nil), prepared.findings...) }

// Plan returns a detached effective configuration and its safety findings.
// Derived settings, such as a default search.log filename, are made explicit.
func (prepared Prepared) Plan() Plan {
	config := prepared.Config()
	if config.Diagnostics.SearchLogFile == "" {
		config.Diagnostics.SearchLogFile = prepared.options.SearchLogFile
	}
	findings := prepared.Findings()
	valid := config.Validate() == nil && len(findingsBySeverity(findings, SeverityViolation)) == 0
	return Plan{Valid: valid, Config: config, Findings: findings}
}

// EncodeYAML writes the plan as one stable YAML document.
func (plan Plan) EncodeYAML(writer io.Writer) error {
	if writer == nil {
		return fmt.Errorf("%w: plan output writer is required", ErrInvalidConfig)
	}
	encoder := yaml.NewEncoder(writer)
	if err := encoder.Encode(plan); err != nil {
		_ = encoder.Close()
		return err
	}
	return encoder.Close()
}

func (prepared Prepared) Search(ctx context.Context, client *splunk.Client) (splunk.Result, error) {
	if client == nil {
		return splunk.Result{}, fmt.Errorf("%w: Splunk client is required", ErrInvalidConfig)
	}
	return client.Search(ctx, prepared.config.Search, cloneOptions(prepared.options))
}

func (prepared Prepared) SearchTo(ctx context.Context, client *splunk.Client, output io.Writer) (splunk.Result, error) {
	if client == nil {
		return splunk.Result{}, fmt.Errorf("%w: Splunk client is required", ErrInvalidConfig)
	}
	return client.SearchTo(ctx, prepared.config.Search, cloneOptions(prepared.options), output)
}

// SearchToFile uses a same-directory temporary file and only replaces the
// configured output after Splunk and local writes succeed.
func (prepared Prepared) SearchToFile(ctx context.Context, client *splunk.Client) (splunk.Result, error) {
	outputFile := strings.TrimSpace(prepared.config.OutputFile)
	if outputFile == "" {
		return splunk.Result{}, fmt.Errorf("%w: output file is required", ErrInvalidConfig)
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputFile), ".querysplunk-result-*")
	if err != nil {
		return splunk.Result{}, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return splunk.Result{}, err
	}
	result, err := prepared.SearchTo(ctx, client, temporary)
	if err != nil {
		_ = temporary.Close()
		return result, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return result, err
	}
	if err := temporary.Close(); err != nil {
		return result, err
	}
	if err := os.Rename(temporaryPath, outputFile); err != nil {
		return result, err
	}
	return result, nil
}

func WriteSkeleton(path string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: config output path is required", ErrInvalidConfig)
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("config file %q already exists; use force to overwrite", path)
		}
		return err
	}
	if _, err := io.WriteString(file, SkeletonConfig); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func HasTimeBounds(search string, dispatch map[string][]string) bool {
	for _, key := range []string{"earliest_time", "latest_time"} {
		for _, value := range dispatch[key] {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return timeModifierPattern.MatchString(search)
}

func DerivedSearchLogFile(outputFile string) string {
	if strings.TrimSpace(outputFile) == "" {
		return "splunk.search.log"
	}
	ext := filepath.Ext(outputFile)
	if ext == "" {
		return outputFile + ".search.log"
	}
	return strings.TrimSuffix(outputFile, ext) + ".search.log"
}

func defaults(config Config) Config {
	if strings.TrimSpace(config.OutputFile) == "" {
		config.OutputFile = "splunkresults.json"
	}
	if strings.TrimSpace(config.Mode) == "" {
		config.Mode = string(splunk.ExecutionModeJob)
	}
	if strings.TrimSpace(config.Results.Endpoint) == "" {
		config.Results.Endpoint = string(splunk.ResultEndpointAuto)
	}
	if strings.TrimSpace(config.Diagnostics.SearchLog) == "" {
		config.Diagnostics.SearchLog = string(splunk.SearchLogModeSummary)
	}
	return config
}

func normalizedPolicy(policy SafetyPolicy) SafetyPolicy {
	if policy.MaxEarliestAgeYears <= 0 {
		policy.MaxEarliestAgeYears = 1
	}
	return policy
}
func findingsBySeverity(findings []Finding, severity FindingSeverity) []Finding {
	selected := make([]Finding, 0)
	for _, finding := range findings {
		if finding.Severity == severity {
			selected = append(selected, finding)
		}
	}
	return selected
}
func addString(params map[string][]string, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		params[key] = append(params[key], value)
	}
}
func addInt(params map[string][]string, key string, value *int) {
	if value != nil {
		params[key] = append(params[key], strconv.Itoa(*value))
	}
}
func cloneConfig(config Config) Config {
	config.Dispatch.RequiredFields = append([]string(nil), config.Dispatch.RequiredFields...)
	return config
}
func cloneOptions(options splunk.SearchOptions) splunk.SearchOptions {
	options.DispatchParams = cloneParams(options.DispatchParams)
	options.ResultParams = cloneParams(options.ResultParams)
	return options
}
func cloneParams(params map[string][]string) map[string][]string {
	if params == nil {
		return nil
	}
	cloned := make(map[string][]string, len(params))
	for key, values := range params {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func earliestValues(search string, dispatch map[string][]string) []string {
	values := make([]string, 0)
	for _, value := range dispatch["earliest_time"] {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	for _, match := range earliestPattern.FindAllStringSubmatch(search, -1) {
		if len(match) >= 3 {
			if value := unquote(match[2]); value != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

func parseEarliest(value string, now time.Time) (time.Time, bool) {
	value = unquote(value)
	if value == "" || strings.EqualFold(value, "now") {
		return time.Time{}, false
	}
	if epoch, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(epoch, 0), true
	}
	if match := relativeEarliestPattern.FindStringSubmatch(value); len(match) >= 3 {
		amount, err := strconv.Atoi(match[1])
		if err != nil {
			return time.Time{}, false
		}
		switch strings.ToLower(match[2]) {
		case "s", "sec", "secs", "second", "seconds":
			return now.Add(-time.Duration(amount) * time.Second), true
		case "m", "min", "mins", "minute", "minutes":
			return now.Add(-time.Duration(amount) * time.Minute), true
		case "h", "hr", "hrs", "hour", "hours":
			return now.Add(-time.Duration(amount) * time.Hour), true
		case "d", "day", "days":
			return now.AddDate(0, 0, -amount), true
		case "w", "week", "weeks":
			return now.AddDate(0, 0, -7*amount), true
		case "mon", "month", "months":
			return now.AddDate(0, -amount, 0), true
		case "q", "qtr", "quarter", "quarters":
			return now.AddDate(0, -3*amount, 0), true
		case "y", "yr", "yrs", "year", "years":
			return now.AddDate(-amount, 0, 0), true
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, now.Location()); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
