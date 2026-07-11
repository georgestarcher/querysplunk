package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	// import for the .env file support
	"github.com/georgestarcher/querysplunk/v2/splunk"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// setup more standard logging format
type logWriter struct {
}

var splTimeModifierPattern = regexp.MustCompile(`(?i)(^|\s)(earliest|latest)\s*=`)
var splEarliestPattern = regexp.MustCompile(`(?i)(^|[\s(])earliest\s*=\s*("[^"]+"|'[^']+'|[^\s|,)]+)`)
var splIndexWildcardPattern = regexp.MustCompile(`(?i)(^|[\s(])index\s*=\s*("[*]"|'[*]'|[*])($|[\s|,)])`)
var relativeEarliestPattern = regexp.MustCompile(`(?i)^-(\d+)(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks|mon|month|months|q|qtr|quarter|quarters|y|yr|yrs|year|years)(@[a-z0-9]+)?$`)

func (writer logWriter) Write(bytes []byte) (int, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return 0, err
	}

	message := []byte(time.Now().UTC().Format("2006-01-02T15:04:05.999Z") + " " + hostname + " splunkquery [DEBUG] " + string(bytes))
	written, err := os.Stdout.Write(message)
	if err != nil {
		return written, err
	}

	// The logger contract expects the original payload length to be reported as written.
	return len(bytes), nil
}

func timeoutFromEnv() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("SPLUNKTIMEOUT"))
	if value == "" {
		return 120 * time.Second, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid SPLUNKTIMEOUT value %q; must be a positive integer (seconds)", value)
	}
	return time.Duration(parsed) * time.Second, nil
}

func tlsVerifyFromEnv() (bool, error) {
	value := strings.TrimSpace(os.Getenv("SPLUNKTLSVERIFY"))
	if value == "" {
		return true, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return true, fmt.Errorf("invalid SPLUNKTLSVERIFY value %q; must be true or false", value)
	}
	return parsed, nil
}

func readSearchFile(path string) (string, error) {
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	search := string(fileContent)
	if strings.TrimSpace(search) == "" {
		return "", fmt.Errorf("SPL query file %q is empty", path)
	}
	return search, nil
}

func runSearchToFile(ctx context.Context, client *splunk.Client, search string, options splunk.SearchOptions, outputFile string) (splunk.Result, error) {
	directory := filepath.Dir(outputFile)
	temporary, err := os.CreateTemp(directory, ".querysplunk-result-*")
	if err != nil {
		return splunk.Result{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return splunk.Result{}, err
	}

	result, err := client.SearchTo(ctx, search, options, temporary)
	if err != nil {
		_ = temporary.Close()
		return result, err
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		_ = temporary.Close()
		return result, err
	}

	output, err := os.Create(outputFile)
	if err != nil {
		_ = temporary.Close()
		return result, err
	}
	_, copyErr := io.Copy(output, temporary)
	closeOutputErr := output.Close()
	closeTemporaryErr := temporary.Close()
	if copyErr != nil {
		return result, copyErr
	}
	if closeOutputErr != nil {
		return result, closeOutputErr
	}
	return result, closeTemporaryErr
}

const skeletonSearchConfig = `app: search
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

func writeSkeletonConfig(path string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("config output path is required")
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
			return fmt.Errorf("config file %q already exists; use -force to overwrite", path)
		}
		return err
	}
	defer file.Close()

	_, err = file.WriteString(skeletonSearchConfig)
	return err
}

type searchConfig struct {
	App         string            `yaml:"app"`
	OutputFile  string            `yaml:"output_file"`
	Mode        string            `yaml:"mode"`
	Search      string            `yaml:"search"`
	Safety      safetyConfig      `yaml:"safety"`
	Dispatch    dispatchConfig    `yaml:"dispatch"`
	Results     resultsConfig     `yaml:"results"`
	Diagnostics diagnosticsConfig `yaml:"diagnostics"`
}

type safetyConfig struct {
	AllowOldEarliest   bool `yaml:"allow_old_earliest"`
	AllowIndexWildcard bool `yaml:"allow_index_wildcard"`
}

type dispatchConfig struct {
	EarliestTime   string   `yaml:"earliest_time"`
	LatestTime     string   `yaml:"latest_time"`
	MaxCount       *int     `yaml:"max_count"`
	StatusBuckets  *int     `yaml:"status_buckets"`
	RequiredFields []string `yaml:"required_fields"`
}

type resultsConfig struct {
	OutputMode string `yaml:"output_mode"`
	Count      *int   `yaml:"count"`
	Offset     *int   `yaml:"offset"`
	Endpoint   string `yaml:"endpoint"`
}

type diagnosticsConfig struct {
	SearchLog     string `yaml:"search_log"`
	SearchLogFile string `yaml:"search_log_file"`
}

func loadSearchConfig(path string) (searchConfig, error) {
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return searchConfig{}, err
	}

	var config searchConfig
	if err := yaml.Unmarshal(fileContent, &config); err != nil {
		return searchConfig{}, err
	}
	if strings.TrimSpace(config.Search) == "" {
		return searchConfig{}, fmt.Errorf("search config %q is missing search content", path)
	}
	executionMode := strings.TrimSpace(config.Mode)
	if executionMode != "" {
		switch splunk.ExecutionMode(executionMode) {
		case splunk.ExecutionModeJob, splunk.ExecutionModeExport:
		default:
			return searchConfig{}, fmt.Errorf("invalid mode value %q; must be one of job, export", executionMode)
		}
	}
	mode := strings.TrimSpace(config.Diagnostics.SearchLog)
	if mode != "" {
		switch splunk.SearchLogMode(mode) {
		case splunk.SearchLogModeOff, splunk.SearchLogModeSummary, splunk.SearchLogModeSave, splunk.SearchLogModeBoth:
		default:
			return searchConfig{}, fmt.Errorf("invalid diagnostics.search_log value %q; must be one of off, summary, save, both", mode)
		}
	}
	endpoint := strings.TrimSpace(config.Results.Endpoint)
	if endpoint != "" {
		switch splunk.ResultEndpointMode(endpoint) {
		case splunk.ResultEndpointAuto, splunk.ResultEndpointV1, splunk.ResultEndpointV2:
		default:
			return searchConfig{}, fmt.Errorf("invalid results.endpoint value %q; must be one of auto, v1, v2", endpoint)
		}
	}
	return config, nil
}

func explicitFlags() map[string]bool {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

func addStringParam(params map[string][]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		params[key] = append(params[key], value)
	}
}

func addIntParam(params map[string][]string, key string, value *int) {
	if value != nil {
		params[key] = append(params[key], strconv.Itoa(*value))
	}
}

func dispatchParams(config dispatchConfig) map[string][]string {
	params := make(map[string][]string)
	addStringParam(params, "earliest_time", config.EarliestTime)
	addStringParam(params, "latest_time", config.LatestTime)
	addIntParam(params, "max_count", config.MaxCount)
	addIntParam(params, "status_buckets", config.StatusBuckets)
	for _, field := range config.RequiredFields {
		addStringParam(params, "rf", field)
	}
	return params
}

func setStringParam(params map[string][]string, key string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s cannot be empty", key)
	}
	params[key] = []string{value}
	return nil
}

func hasParamValue(params map[string][]string, key string) bool {
	for _, value := range params[key] {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func hasSearchTimeBounds(search string, dispatchParams map[string][]string) bool {
	if hasParamValue(dispatchParams, "earliest_time") || hasParamValue(dispatchParams, "latest_time") {
		return true
	}
	return splTimeModifierPattern.MatchString(search)
}

func unquoteSearchValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}

func splEarliestValues(search string, dispatchParams map[string][]string) []string {
	var values []string
	for _, value := range dispatchParams["earliest_time"] {
		value = strings.TrimSpace(value)
		if value != "" {
			values = append(values, value)
		}
	}
	for _, match := range splEarliestPattern.FindAllStringSubmatch(search, -1) {
		if len(match) >= 3 {
			value := unquoteSearchValue(match[2])
			if value != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

func parseSplunkEarliest(value string, now time.Time) (time.Time, bool) {
	value = unquoteSearchValue(value)
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
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.ParseInLocation(layout, value, now.Location()); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func safetyViolations(search string, dispatchParams map[string][]string, now time.Time, allowOldEarliest bool, allowIndexWildcard bool) []string {
	var violations []string
	if !allowIndexWildcard && splIndexWildcardPattern.MatchString(search) {
		violations = append(violations, "search uses index=*, which can unexpectedly fan out across a Splunk deployment; rerun with -allow-index-wildcard to acknowledge this risk")
	}
	if !allowOldEarliest {
		cutoff := now.AddDate(-1, 0, 0)
		for _, value := range splEarliestValues(search, dispatchParams) {
			parsed, ok := parseSplunkEarliest(value, now)
			if ok && parsed.Before(cutoff) {
				violations = append(violations, fmt.Sprintf("earliest=%s is older than the default one-year safety limit; rerun with -allow-old-earliest to acknowledge this risk", value))
			}
		}
	}
	return violations
}

func resultParams(config resultsConfig) map[string][]string {
	params := make(map[string][]string)
	addStringParam(params, "output_mode", config.OutputMode)
	addIntParam(params, "count", config.Count)
	addIntParam(params, "offset", config.Offset)
	return params
}

func usage() {
	output := flag.CommandLine.Output()
	_, _ = fmt.Fprintln(output, `Usage:
  querysplunk [options]

Run a Splunk search from a plain SPL file or from a structured YAML config.

Examples:
  querysplunk -q query.txt -o splunkresults.json
  querysplunk -q query.txt -earliest=-15m -latest=now
  querysplunk -config search.yml
  querysplunk -write-config search.yml
  querysplunk -write-config search.yml -force

Authentication and connection settings are read from environment variables:
  SPLUNKBASEURL
  SPLUNKTOKEN
  SPLUNKUSERNAME / SPLUNKPASSWORD
  SPLUNKTLSVERIFY
  SPLUNKTIMEOUT
  SPLUNKAPP

Use -e to load those values from .env in the working directory.

Safety controls block earliest values older than one year and explicit index=*
searches unless acknowledged with -allow-old-earliest, -allow-index-wildcard,
or YAML safety.allow_old_earliest / safety.allow_index_wildcard.

Options:`)
	flag.PrintDefaults()
}

func main() {
	var queryFile string
	var outputFile string
	var useEnvFile bool
	var appContext string
	var configFile string
	var writeConfigFile string
	var forceWrite bool
	var earliestTime string
	var latestTime string
	var allowOldEarliest bool
	var allowIndexWildcard bool

	log.SetFlags(0)
	log.SetOutput(new(logWriter))
	flag.Usage = usage

	flag.BoolVar(&useEnvFile, "e", false, "Load Splunk connection settings from .env")
	flag.StringVar(&appContext, "app", "", "Override Splunk app context / namespace for the search")
	flag.StringVar(&configFile, "config", "", "Run a structured YAML search config")
	flag.StringVar(&writeConfigFile, "write-config", "", "Write a starter YAML search config and exit")
	flag.StringVar(&earliestTime, "earliest", "", "Set dispatch earliest_time, such as -15m or 2026-07-10T00:00:00")
	flag.StringVar(&latestTime, "latest", "", "Set dispatch latest_time, such as now")
	flag.BoolVar(&allowOldEarliest, "allow-old-earliest", false, "Allow earliest times older than the default one-year safety limit")
	flag.BoolVar(&allowIndexWildcard, "allow-index-wildcard", false, "Allow searches that explicitly use index=*")
	flag.BoolVar(&forceWrite, "force", false, "Allow -write-config to overwrite an existing file")
	flag.StringVar(&queryFile, "q", "query.txt", "Read the SPL search from this plain text file")
	flag.StringVar(&outputFile, "o", "splunkresults.json", "Write Splunk results to this file")
	flag.Parse()
	flagsSet := explicitFlags()

	if writeConfigFile != "" {
		if err := writeSkeletonConfig(writeConfigFile, forceWrite); err != nil {
			log.Fatal(err)
		}
		log.Printf("SUCCESS: Wrote starter config to %s", writeConfigFile)
		return
	}

	if useEnvFile {
		if err := godotenv.Load(); err != nil {
			log.Fatal("ERROR: could not load .env file")
		}
	}

	var config searchConfig
	if configFile != "" {
		var err error
		config, err = loadSearchConfig(configFile)
		if err != nil {
			log.Fatal(err)
		}
		if !flagsSet["app"] {
			appContext = config.App
		}
		if !flagsSet["o"] && strings.TrimSpace(config.OutputFile) != "" {
			outputFile = config.OutputFile
		}
	}

	if appContext == "" {
		appContext = os.Getenv("SPLUNKAPP")
	}
	appContext = strings.TrimSpace(appContext)

	var splunkQueryString string
	var err error
	if configFile != "" {
		splunkQueryString = config.Search
	} else {
		splunkQueryString, err = readSearchFile(queryFile)
		if err != nil {
			log.Fatal(err)
		}
	}

	username := os.Getenv("SPLUNKUSERNAME")
	password := os.Getenv("SPLUNKPASSWORD")
	baseURL := strings.TrimRight(os.Getenv("SPLUNKBASEURL"), "/")
	splunkToken := os.Getenv("SPLUNKTOKEN")

	if splunkToken == "" && (username == "" || password == "") {
		log.Fatal("ERROR: missing SPLUNKUSERNAME and/or SPLUNKPASSWORD when SPLUNKTOKEN is not set")
	}
	if baseURL == "" {
		log.Fatal("ERROR: Missing SPLUNKBASEURL")
	}

	tlsVerify, err := tlsVerifyFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	timeout, err := timeoutFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := splunk.NewClient(splunk.Config{
		App:                appContext,
		Username:           username,
		Password:           password,
		Token:              splunkToken,
		BaseURL:            baseURL,
		InsecureSkipVerify: !tlsVerify,
		Timeout:            timeout,
	})
	if err != nil {
		log.Fatalf("ERROR: Invalid Splunk configuration: %s", err)
	}
	defer client.Close()

	if err = client.Authenticate(ctx); err != nil {
		log.Fatalf("ERROR: Couldn't login to Splunk: %s", err)
	}

	options := splunk.SearchOptions{}
	if configFile != "" {
		options.DispatchParams = dispatchParams(config.Dispatch)
		options.ResultParams = resultParams(config.Results)
		if strings.TrimSpace(config.Results.Endpoint) != "" {
			options.ResultEndpoint = splunk.ResultEndpointMode(strings.TrimSpace(config.Results.Endpoint))
		}
		if strings.TrimSpace(config.Mode) != "" {
			options.ExecutionMode = splunk.ExecutionMode(strings.TrimSpace(config.Mode))
		}
		if strings.TrimSpace(config.Diagnostics.SearchLog) != "" {
			options.SearchLog = splunk.SearchLogMode(strings.TrimSpace(config.Diagnostics.SearchLog))
		}
		options.SearchLogFile = strings.TrimSpace(config.Diagnostics.SearchLogFile)
	}
	if options.DispatchParams == nil {
		options.DispatchParams = make(map[string][]string)
	}
	if flagsSet["earliest"] {
		if err := setStringParam(options.DispatchParams, "earliest_time", earliestTime); err != nil {
			log.Fatal(err)
		}
	}
	if flagsSet["latest"] {
		if err := setStringParam(options.DispatchParams, "latest_time", latestTime); err != nil {
			log.Fatal(err)
		}
	}
	if configFile != "" {
		allowOldEarliest = allowOldEarliest || config.Safety.AllowOldEarliest
		allowIndexWildcard = allowIndexWildcard || config.Safety.AllowIndexWildcard
	}
	if !hasSearchTimeBounds(splunkQueryString, options.DispatchParams) {
		log.Print("WARN: no SPL earliest/latest modifier or dispatch earliest_time/latest_time was provided; Splunk REST searches can run over all time")
	}
	if violations := safetyViolations(splunkQueryString, options.DispatchParams, time.Now(), allowOldEarliest, allowIndexWildcard); len(violations) > 0 {
		for _, violation := range violations {
			log.Printf("WARN: %s", violation)
		}
		log.Fatal("ERROR: search blocked by safety controls")
	}
	if _, err = runSearchToFile(ctx, client, splunkQueryString, options, outputFile); err != nil {
		log.Fatalf("ERROR: %s", err)
	}

	log.Print("SUCCESS: Query Completed")
}
