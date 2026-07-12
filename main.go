package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	// import for the .env file support
	querypkg "github.com/georgestarcher/querysplunk/v2/query"
	"github.com/georgestarcher/querysplunk/v2/splunk"
	"github.com/joho/godotenv"
)

var (
	version = "dev"
	commit  = "unknown"
)

func versionString() string {
	return fmt.Sprintf("querysplunk version=%s commit=%s", version, commit)
}

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

func newSplunkClientFromEnvironment(app string, logger *slog.Logger) (*splunk.Client, time.Duration, error) {
	username := os.Getenv("SPLUNKUSERNAME")
	password := os.Getenv("SPLUNKPASSWORD")
	baseURL := strings.TrimRight(os.Getenv("SPLUNKBASEURL"), "/")
	token := os.Getenv("SPLUNKTOKEN")
	if token == "" && (username == "" || password == "") {
		return nil, 0, errors.New("missing SPLUNKUSERNAME and/or SPLUNKPASSWORD when SPLUNKTOKEN is not set")
	}
	if baseURL == "" {
		return nil, 0, errors.New("missing SPLUNKBASEURL")
	}
	tlsVerify, err := tlsVerifyFromEnv()
	if err != nil {
		return nil, 0, err
	}
	timeout, err := timeoutFromEnv()
	if err != nil {
		return nil, 0, err
	}
	client, err := splunk.NewClient(splunk.Config{App: strings.TrimSpace(app), Username: username, Password: password, Token: token, BaseURL: baseURL, InsecureSkipVerify: !tlsVerify, Timeout: timeout, Logger: logger})
	if err != nil {
		return nil, 0, err
	}
	return client, timeout, nil
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

const skeletonSearchConfig = querypkg.SkeletonConfig

func writeSkeletonConfig(path string, force bool) error {
	return querypkg.WriteSkeleton(path, force)
}

type searchConfig = querypkg.Config
type safetyConfig = querypkg.Safety
type dispatchConfig = querypkg.Dispatch
type resultsConfig = querypkg.Results

func loadSearchConfig(path string) (searchConfig, error) {
	return querypkg.LoadFile(path)
}

func validateSearchConfig(path string, overrides querypkg.Overrides, output io.Writer) error {
	config, err := querypkg.LoadFile(path)
	if err != nil {
		return err
	}
	if overrides.App == nil && strings.TrimSpace(config.App) == "" {
		app := strings.TrimSpace(os.Getenv("SPLUNKAPP"))
		if app != "" {
			overrides.App = &app
		}
	}
	prepared, prepareErr := querypkg.Prepare(config, overrides, querypkg.SafetyPolicy{})
	if err := prepared.Plan().EncodeYAML(output); err != nil {
		return err
	}
	return prepareErr
}

func runConfigValidation(path string, overrides querypkg.Overrides, output, errorOutput io.Writer) int {
	if err := validateSearchConfig(path, overrides, output); err != nil {
		_, _ = fmt.Fprintf(errorOutput, "ERROR: %v\n", err)
		return 1
	}
	return 0
}

func validateConfigModes(configFile, validateConfigFile, writeConfigFile string) error {
	modes := 0
	for _, path := range []string{configFile, validateConfigFile, writeConfigFile} {
		if strings.TrimSpace(path) != "" {
			modes++
		}
	}
	if modes > 1 {
		return errors.New("-config, -validate-config, and -write-config are mutually exclusive")
	}
	return nil
}

func explicitFlags() map[string]bool {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

func dispatchParams(config dispatchConfig) map[string][]string {
	options, _ := (querypkg.Config{Search: "placeholder", Dispatch: config}).SearchOptions()
	return options.DispatchParams
}

func setStringParam(params map[string][]string, key string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s cannot be empty", key)
	}
	params[key] = []string{value}
	return nil
}

func hasSearchTimeBounds(search string, dispatchParams map[string][]string) bool {
	return querypkg.HasTimeBounds(search, dispatchParams)
}

func safetyViolations(search string, dispatchParams map[string][]string, now time.Time, allowOldEarliest bool, allowIndexWildcard bool) []string {
	config := querypkg.Config{Search: search, Safety: querypkg.Safety{AllowOldEarliest: allowOldEarliest, AllowIndexWildcard: allowIndexWildcard}, Dispatch: querypkg.Dispatch{EarliestTime: firstParam(dispatchParams, "earliest_time"), LatestTime: firstParam(dispatchParams, "latest_time")}}
	findings := querypkg.Analyze(config, querypkg.SafetyPolicy{Now: func() time.Time { return now }})
	violations := make([]string, 0)
	for _, finding := range findings {
		if finding.Severity == querypkg.SeverityViolation {
			violations = append(violations, finding.Message)
		}
	}
	return violations
}

func resultParams(config resultsConfig) map[string][]string {
	options, _ := (querypkg.Config{Search: "placeholder", Results: config}).SearchOptions()
	return options.ResultParams
}

func firstParam(params map[string][]string, key string) string {
	for _, value := range params[key] {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func usage() {
	output := flag.CommandLine.Output()
	_, _ = fmt.Fprintln(output, `Usage:
  querysplunk [options]

Run a Splunk search or reconnect to an existing Splunk search job.

Examples:
  querysplunk -version
  querysplunk -validate-config search.yml
  querysplunk -job-sid 1258421375.19 -job-action status
  querysplunk -job-sid 1258421375.19 -job-action wait
  querysplunk -job-sid 1258421375.19 -job-action results -o results.json
  querysplunk -job-sid 1258421375.19 -job-action search-log
  querysplunk -job-sid 1258421375.19 -job-action cancel
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
	var validateConfigFile string
	var forceWrite bool
	var earliestTime string
	var latestTime string
	var allowOldEarliest bool
	var allowIndexWildcard bool
	var showVersion bool
	var jobID string
	var jobAction string

	log.SetFlags(0)
	log.SetOutput(new(logWriter))
	flag.Usage = usage

	flag.BoolVar(&useEnvFile, "e", false, "Load Splunk connection settings from .env")
	flag.StringVar(&appContext, "app", "", "Override Splunk app context / namespace for the search")
	flag.StringVar(&configFile, "config", "", "Run a structured YAML search config")
	flag.StringVar(&validateConfigFile, "validate-config", "", "Validate a YAML search config offline and print its effective plan")
	flag.StringVar(&writeConfigFile, "write-config", "", "Write a starter YAML search config and exit")
	flag.StringVar(&earliestTime, "earliest", "", "Set dispatch earliest_time, such as -15m or 2026-07-10T00:00:00")
	flag.StringVar(&latestTime, "latest", "", "Set dispatch latest_time, such as now")
	flag.BoolVar(&allowOldEarliest, "allow-old-earliest", false, "Allow earliest times older than the default one-year safety limit")
	flag.BoolVar(&allowIndexWildcard, "allow-index-wildcard", false, "Allow searches that explicitly use index=*")
	flag.BoolVar(&showVersion, "version", false, "Print version and build metadata, then exit")
	flag.StringVar(&jobID, "job-sid", "", "Use an existing Splunk search job ID")
	flag.StringVar(&jobAction, "job-action", "", "Act on -job-sid: status, wait, results, search-log, or cancel")
	flag.BoolVar(&forceWrite, "force", false, "Allow -write-config to overwrite an existing file")
	flag.StringVar(&queryFile, "q", "query.txt", "Read the SPL search from this plain text file")
	flag.StringVar(&outputFile, "o", "splunkresults.json", "Write Splunk results to this file")
	flag.Parse()
	if showVersion {
		fmt.Println(versionString())
		return
	}
	flagsSet := explicitFlags()
	jobOptions := jobCLIOptions{Action: jobAction, JobID: jobID, OutputFile: outputFile, OutputExplicit: flagsSet["o"]}
	jobMode, err := validateJobMode(jobOptions, flagsSet, configFile, validateConfigFile, writeConfigFile)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(2)
	}
	if err := validateConfigModes(configFile, validateConfigFile, writeConfigFile); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(2)
	}

	if writeConfigFile != "" {
		if err := writeSkeletonConfig(writeConfigFile, forceWrite); err != nil {
			log.Fatal(err)
		}
		log.Printf("SUCCESS: Wrote starter config to %s", writeConfigFile)
		return
	}
	if validateConfigFile != "" {
		overrides := querypkg.Overrides{AllowOldEarliest: allowOldEarliest, AllowIndexWildcard: allowIndexWildcard}
		if flagsSet["app"] {
			overrides.App = &appContext
		}
		if flagsSet["o"] {
			overrides.OutputFile = &outputFile
		}
		if flagsSet["earliest"] {
			overrides.EarliestTime = &earliestTime
		}
		if flagsSet["latest"] {
			overrides.LatestTime = &latestTime
		}
		if status := runConfigValidation(validateConfigFile, overrides, os.Stdout, os.Stderr); status != 0 {
			os.Exit(status)
		}
		return
	}

	if useEnvFile {
		if err := godotenv.Load(); err != nil {
			log.Fatal("ERROR: could not load .env file")
		}
	}
	if jobMode {
		if appContext == "" {
			appContext = os.Getenv("SPLUNKAPP")
		}
		client, timeout, err := newSplunkClientFromEnvironment(appContext, nil)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := runJobAction(ctx, client, jobOptions, os.Stdout); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		return
	}

	var config querypkg.Config
	if configFile != "" {
		var err error
		config, err = querypkg.LoadFile(configFile)
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

	if configFile == "" {
		splunkQueryString, readErr := readSearchFile(queryFile)
		err = readErr
		if err != nil {
			log.Fatal(err)
		}
		config = querypkg.Config{Search: splunkQueryString, OutputFile: outputFile}
	}

	overrides := querypkg.Overrides{AllowOldEarliest: allowOldEarliest, AllowIndexWildcard: allowIndexWildcard}
	if flagsSet["app"] || strings.TrimSpace(config.App) == "" {
		overrides.App = &appContext
	}
	if flagsSet["o"] || strings.TrimSpace(config.OutputFile) == "" {
		overrides.OutputFile = &outputFile
	}
	if flagsSet["earliest"] {
		overrides.EarliestTime = &earliestTime
	}
	if flagsSet["latest"] {
		overrides.LatestTime = &latestTime
	}
	prepared, err := querypkg.Prepare(config, overrides, querypkg.SafetyPolicy{})
	for _, finding := range prepared.Findings() {
		if finding.Severity == querypkg.SeverityWarning || finding.Severity == querypkg.SeverityAcknowledged {
			log.Printf("WARN: %s", finding.Message)
		}
	}
	if err != nil {
		var violation *querypkg.ViolationError
		if errors.As(err, &violation) {
			for _, finding := range violation.Findings {
				log.Printf("WARN: %s", finding.Message)
			}
			log.Fatal("ERROR: search blocked by safety controls")
		}
		log.Fatal(err)
	}
	preparedConfig := prepared.Config()
	client, timeout, err := newSplunkClientFromEnvironment(preparedConfig.App, slog.New(slog.NewTextHandler(new(logWriter), &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err != nil {
		log.Fatalf("ERROR: Invalid Splunk configuration: %s", err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err = client.Authenticate(ctx); err != nil {
		log.Fatalf("ERROR: Couldn't login to Splunk: %s", err)
	}

	if _, err = prepared.SearchToFile(ctx, client); err != nil {
		log.Fatalf("ERROR: %s", err)
	}

	log.Print("SUCCESS: Query Completed")
}
