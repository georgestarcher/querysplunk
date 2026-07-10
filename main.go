package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	// import for the .env file support
	"github.com/georgestarcher/querysplunk/splunk"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// setup more standard logging format
type logWriter struct {
}

var splTimeModifierPattern = regexp.MustCompile(`(?i)(^|\s)(earliest|latest)\s*=`)

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

const skeletonSearchConfig = `app: search
output_file: splunkresults.json
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
  output_mode: json
  count: 0
  offset: 0

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
	Search      string            `yaml:"search"`
	Dispatch    dispatchConfig    `yaml:"dispatch"`
	Results     resultsConfig     `yaml:"results"`
	Diagnostics diagnosticsConfig `yaml:"diagnostics"`
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
	mode := strings.TrimSpace(config.Diagnostics.SearchLog)
	if mode != "" {
		switch splunk.SearchLogMode(mode) {
		case splunk.SearchLogModeOff, splunk.SearchLogModeSummary, splunk.SearchLogModeSave, splunk.SearchLogModeBoth:
		default:
			return searchConfig{}, fmt.Errorf("invalid diagnostics.search_log value %q; must be one of off, summary, save, both", mode)
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

Use -e to load those values from a .env file in the working directory.

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

	log.SetFlags(0)
	log.SetOutput(new(logWriter))
	flag.Usage = usage

	flag.BoolVar(&useEnvFile, "e", false, "Load Splunk connection settings from .env")
	flag.StringVar(&appContext, "app", "", "Override Splunk app context / namespace for the search")
	flag.StringVar(&configFile, "config", "", "Run a structured YAML search config")
	flag.StringVar(&writeConfigFile, "write-config", "", "Write a starter YAML search config and exit")
	flag.StringVar(&earliestTime, "earliest", "", "Set dispatch earliest_time, such as -15m or 2026-07-10T00:00:00")
	flag.StringVar(&latestTime, "latest", "", "Set dispatch latest_time, such as now")
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

	conn := splunk.SplunkConnection{
		AppContext: appContext,
		Username:   username,
		Password:   password,
		AuthToken:  splunkToken,
		BaseURL:    baseURL,
		TLSVerify:  tlsVerify,
		Timeout:    timeout,
	}

	if err = conn.Login(ctx); err != nil {
		log.Fatalf("ERROR: Couldn't login to Splunk: %s", err)
	}

	splunkQuery := splunk.SplunkQuery{Query: splunkQueryString}
	options := splunk.DefaultDispatchOptions(outputFile)
	if configFile != "" {
		options.DispatchParams = dispatchParams(config.Dispatch)
		options.ResultParams = resultParams(config.Results)
		if strings.TrimSpace(config.Diagnostics.SearchLog) != "" {
			options.SearchLogMode = splunk.SearchLogMode(strings.TrimSpace(config.Diagnostics.SearchLog))
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
	if !hasSearchTimeBounds(splunkQueryString, options.DispatchParams) {
		log.Print("WARN: no SPL earliest/latest modifier or dispatch earliest_time/latest_time was provided; Splunk REST searches can run over all time")
	}
	if err = conn.DispatchQueryWithOptions(ctx, &splunkQuery, options); err != nil {
		log.Fatalf("ERROR: %s", err)
	}

	log.Print("SUCCESS: Query Completed")
}
