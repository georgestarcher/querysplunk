package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
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

func resultParams(config resultsConfig) map[string][]string {
	params := make(map[string][]string)
	addStringParam(params, "output_mode", config.OutputMode)
	addIntParam(params, "count", config.Count)
	addIntParam(params, "offset", config.Offset)
	return params
}

func main() {
	var queryFile string
	var outputFile string
	var useEnvFile bool
	var appContext string
	var configFile string
	var writeConfigFile string
	var forceWrite bool

	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	flag.BoolVar(&useEnvFile, "e", false, "Use .env file")
	flag.StringVar(&appContext, "app", "", "Splunk app context (namespace) for query execution")
	flag.StringVar(&configFile, "config", "", "Read structured search config from this YAML file")
	flag.StringVar(&writeConfigFile, "write-config", "", "Write a starter structured YAML config file and exit")
	flag.BoolVar(&forceWrite, "force", false, "Allow -write-config to overwrite an existing file")
	flag.StringVar(&queryFile, "q", "query.txt", "Read the SPL search from this file")
	flag.StringVar(&outputFile, "o", "splunkresults.json", "Write Splunk results to this JSON file")
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
	if err = conn.DispatchQueryWithOptions(ctx, &splunkQuery, options); err != nil {
		log.Fatalf("ERROR: %s", err)
	}

	log.Print("SUCCESS: Query Completed")
}
