package splunk

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Data Structures

type SplunkConnection struct {
	Username, Password, BaseURL string
	AppContext                  string
	SessionKey                  SessionKey
	AuthToken                   string
	TLSVerify                   bool
	Timeout                     time.Duration
	PollInterval                time.Duration
}

type SessionKey struct {
	Value string `json:"sessionKey"`
}

type SplunkJob struct {
	XMLName xml.Name `xml:"response"`
	Text    string   `xml:",chardata"`
	Sid     string   `xml:"sid"`
}

type SplunkQuery struct {
	Query          string
	Job            SplunkJob
	State          string
	Results        []byte
	LogDiagnostics JobLogDiagnostics
	SearchLogRead  bool
	SearchLogFile  string
}

type SplunkJobStatus struct {
	Entry []struct {
		Content map[string]any `json:"content"`
	} `json:"entry"`
}

type JobLogDiagnostics struct {
	ExecutionDuration string
	Warnings          []string
	Errors            []string
}

type HTTPStatusError struct {
	StatusCode int
	Status     string
	URL        string
	Body       string
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("request failed with status %s for %s: %s", err.Status, err.URL, err.Body)
}

type SearchLogMode string

const (
	SearchLogModeOff     SearchLogMode = "off"
	SearchLogModeSummary SearchLogMode = "summary"
	SearchLogModeSave    SearchLogMode = "save"
	SearchLogModeBoth    SearchLogMode = "both"
)

type ResultEndpointMode string

const (
	ResultEndpointAuto ResultEndpointMode = "auto"
	ResultEndpointV1   ResultEndpointMode = "v1"
	ResultEndpointV2   ResultEndpointMode = "v2"
)

type DispatchOptions struct {
	OutputFile         string
	DispatchParams     map[string][]string
	ResultParams       map[string][]string
	ResultEndpointMode ResultEndpointMode
	SearchLogMode      SearchLogMode
	SearchLogFile      string
}

const (
	dispatchStateDone           = "DONE"
	dispatchStateFailed         = "FAILED"
	dispatchStateCancelled      = "CANCELLED"
	dispatchStateInternalCancel = "INTERNAL_CANCEL"
	dispatchStateUserCancel     = "USER_CANCEL"
	dispatchStateBadInputCancel = "BAD_INPUT_CANCEL"
	dispatchStateQuit           = "QUIT"
	dispatchStatePause          = "PAUSE"
	dispatchStatePaused         = "PAUSED"
	defaultPollInterval         = time.Second
	cancelTimeout               = 10 * time.Second
	maxDiagnosticLines          = 20
	maxDiagnosticLineLength     = 500
)

var durationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:execution[_ ]?time|run[_ ]?duration|run[_ ]?time|total[_ ]?run[_ ]?time|elapsed(?:[_ ]?time)?|duration)\b[=:\s]+([0-9]+(?:\.[0-9]+)?\s*(?:seconds|second|secs|sec|minutes|minute|mins|min|ms|s|m)?)`),
	regexp.MustCompile(`(?i)\bcompleted\s+in\s+([0-9]+(?:\.[0-9]+)?\s*(?:seconds|second|secs|sec|minutes|minute|mins|min|ms|s|m))`),
}

var (
	errorLogLevelPattern = regexp.MustCompile(`(?i)(?:^|\s)(?:ERROR|FATAL)(?:\s|$|:)`)
	warnLogLevelPattern  = regexp.MustCompile(`(?i)(?:^|\s)WARN(?:ING)?(?:\s|$|:)`)
)

// Web Methods

func (conn SplunkConnection) httpClient() *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !conn.TLSVerify},
	}
	client := &http.Client{Transport: tr, Timeout: conn.Timeout}
	return client
}

func (conn SplunkConnection) httpGet(ctx context.Context, url string, data *url.Values) (string, error) {
	return conn.httpCall(ctx, url, http.MethodGet, data)
}

func (conn SplunkConnection) httpPost(ctx context.Context, url string, data *url.Values) (string, error) {
	return conn.httpCall(ctx, url, http.MethodPost, data)
}

func (conn SplunkConnection) httpCall(ctx context.Context, requestURL string, method string, data *url.Values) (string, error) {
	client := conn.httpClient()

	var payload io.Reader
	if method == http.MethodGet && data != nil {
		parsedURL, err := url.Parse(requestURL)
		if err != nil {
			return "", err
		}
		parsedQuery := parsedURL.Query()
		for key, values := range *data {
			for _, value := range values {
				parsedQuery.Add(key, value)
			}
		}
		parsedURL.RawQuery = parsedQuery.Encode()
		requestURL = parsedURL.String()
	} else if data != nil {
		payload = bytes.NewBufferString(data.Encode())
	}

	request, err := http.NewRequestWithContext(ctx, method, requestURL, payload)
	if err != nil {
		return "", err
	}
	if method == http.MethodPost && data != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	conn.addAuthHeader(request)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return "", readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &HTTPStatusError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			URL:        safeURLForLog(requestURL),
			Body:       responseBodyForError(body),
		}
	}
	return string(body), nil
}

func responseBodyForError(body []byte) string {
	const maxErrorBodyBytes = 2048

	trimmedBody := strings.TrimSpace(string(body))
	if len(trimmedBody) <= maxErrorBodyBytes {
		return trimmedBody
	}
	return fmt.Sprintf("%s... [truncated %d bytes]", trimmedBody[:maxErrorBodyBytes], len(trimmedBody)-maxErrorBodyBytes)
}

func safeURLForLog(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "url=<invalid>"
	}

	path := parsedURL.EscapedPath()
	if path == "" {
		path = "/"
	}

	return fmt.Sprintf("scheme=%s host=%s path=%s", parsedURL.Scheme, parsedURL.Host, path)
}

func (conn SplunkConnection) addAuthHeader(request *http.Request) {
	// use auth token first if provided. then session key if already obtained. login with credentials last
	if conn.AuthToken != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", conn.AuthToken))
	} else if conn.SessionKey.Value != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Splunk %s", conn.SessionKey.Value))
	} else {
		request.SetBasicAuth(conn.Username, conn.Password)
	}
}

// Splunk Methods

// Login connects to the Splunk server and retrieves a session key.
func (conn *SplunkConnection) Login(ctx context.Context) error {
	if conn.AuthToken != "" {
		return conn.ValidateAuth(ctx)
	}
	if conn.Username == "" || conn.Password == "" {
		return errors.New("SPLUNKUSERNAME and SPLUNKPASSWORD are required when SPLUNKTOKEN is not set")
	}

	data := make(url.Values)
	data.Add("username", conn.Username)
	data.Add("password", conn.Password)
	data.Add("output_mode", "json")
	response, err := conn.httpPost(ctx, fmt.Sprintf("%s/services/auth/login", conn.BaseURL), &data)
	if err != nil {
		return err
	}
	if strings.Contains(response, "Login failed") || strings.Contains(response, "Unauthorized") {
		return fmt.Errorf("%s", response)
	}

	var key SessionKey
	if err := json.Unmarshal([]byte(response), &key); err != nil {
		return err
	}
	if key.Value == "" {
		return errors.New("could not parse sessionKey from login response")
	}
	conn.SessionKey = key
	return nil
}

// ValidateAuth checks that the configured authentication can access the Splunk REST API.
func (conn SplunkConnection) ValidateAuth(ctx context.Context) error {
	data := make(url.Values)
	data.Add("output_mode", "json")
	_, err := conn.httpGet(ctx, fmt.Sprintf("%s/services/authentication/current-context", conn.BaseURL), &data)
	if err != nil {
		return fmt.Errorf("validate splunk authentication: %w", err)
	}
	return nil
}

// Return URL string formatted with job sid.
func (conn SplunkConnection) jobURL(query *SplunkQuery) string {
	return fmt.Sprintf("%s/services/search/jobs/%s", conn.BaseURL, query.Job.Sid)
}

func (conn SplunkConnection) pollInterval() time.Duration {
	if conn.PollInterval > 0 {
		return conn.PollInterval
	}
	return defaultPollInterval
}

// Check on job status until terminal state or context deadline.
func (conn SplunkConnection) jobStatus(ctx context.Context, query *SplunkQuery) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data.Add("output_mode", "json")
	query.State = "DISPATCHED"
	ticker := time.NewTicker(conn.pollInterval())
	defer ticker.Stop()
	lastLoggedState := ""

	for {
		select {
		case <-ctx.Done():
			query.State = "CANCELLED"
			if query.Job.Sid != "" {
				if err := conn.cancelJob(query); err != nil {
					log.Printf("WARN: could not cancel Splunk search job %s after local context ended: %v", query.Job.Sid, err)
				}
			}
			return ctx.Err()
		case <-ticker.C:
		}

		response, err := conn.httpGet(ctx, conn.jobURL(query), &data)
		if err != nil {
			query.State = "ERROR"
			return err
		}

		status, err := parseJobStatus(response)
		if err != nil {
			query.State = "ERROR"
			return err
		}

		if status.DispatchState == "" {
			continue
		}
		query.State = status.DispatchState
		if query.State != lastLoggedState {
			logJobProgress(query.Job.Sid, status)
			lastLoggedState = query.State
		}
		if query.State == dispatchStateDone {
			return nil
		}
		if isTerminalErrorState(query.State) {
			return fmt.Errorf("splunk job ended in %s state", query.State)
		}
	}
}

type jobStatusContent struct {
	DispatchState string
	DoneProgress  string
	ScanCount     string
	EventCount    string
	ResultCount   string
}

func parseJobStatus(response string) (jobStatusContent, error) {
	var payload SplunkJobStatus
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		return jobStatusContent{}, err
	}
	if len(payload.Entry) == 0 {
		return jobStatusContent{}, nil
	}

	content := payload.Entry[0].Content
	return jobStatusContent{
		DispatchState: stringValue(content["dispatchState"]),
		DoneProgress:  stringValue(content["doneProgress"]),
		ScanCount:     stringValue(content["scanCount"]),
		EventCount:    stringValue(content["eventCount"]),
		ResultCount:   stringValue(content["resultCount"]),
	}, nil
}

func stringValue(value any) string {
	switch typedValue := value.(type) {
	case string:
		return typedValue
	case float64:
		return fmt.Sprintf("%g", typedValue)
	case bool:
		return fmt.Sprintf("%t", typedValue)
	default:
		return ""
	}
}

func isTerminalErrorState(state string) bool {
	switch state {
	case dispatchStateFailed, dispatchStateCancelled, dispatchStateInternalCancel, dispatchStateUserCancel, dispatchStateBadInputCancel, dispatchStateQuit, dispatchStatePause, dispatchStatePaused:
		return true
	default:
		return false
	}
}

func logJobProgress(sid string, status jobStatusContent) {
	var details []string
	if status.DoneProgress != "" {
		details = append(details, "doneProgress="+status.DoneProgress)
	}
	if status.ScanCount != "" {
		details = append(details, "scanCount="+status.ScanCount)
	}
	if status.EventCount != "" {
		details = append(details, "eventCount="+status.EventCount)
	}
	if status.ResultCount != "" {
		details = append(details, "resultCount="+status.ResultCount)
	}
	if len(details) == 0 {
		log.Printf("INFO: Splunk search job %s state=%s", sid, status.DispatchState)
		return
	}
	log.Printf("INFO: Splunk search job %s state=%s %s", sid, status.DispatchState, strings.Join(details, " "))
}

func (conn SplunkConnection) cancelJob(query *SplunkQuery) error {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()

	data := make(url.Values)
	data = conn.namespaceValues(data)
	data.Add("action", "cancel")
	_, err := conn.httpPost(ctx, fmt.Sprintf("%s/control", conn.jobURL(query)), &data)
	return err
}

func DefaultDispatchOptions(outputFile string) DispatchOptions {
	return DispatchOptions{
		OutputFile:         outputFile,
		ResultEndpointMode: ResultEndpointAuto,
		SearchLogMode:      SearchLogModeSummary,
	}
}

func (options DispatchOptions) normalized() DispatchOptions {
	if options.SearchLogMode == "" {
		options.SearchLogMode = SearchLogModeSummary
	}
	if options.ResultEndpointMode == "" {
		options.ResultEndpointMode = ResultEndpointAuto
	}
	return options
}

func addParams(values url.Values, params map[string][]string) url.Values {
	if values == nil {
		values = make(url.Values)
	}
	for key, paramValues := range params {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		for _, value := range paramValues {
			values.Add(trimmedKey, value)
		}
	}
	return values
}

// Write results bytes to file as unmodified JSON.
// Something else like python etc can be used on the saved API response.
func (conn SplunkConnection) writeResults(query *SplunkQuery, outputfile string) error {
	return os.WriteFile(outputfile, query.Results, 0644)
}

func canFallbackResultEndpoint(err error) bool {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusMethodNotAllowed || statusErr.StatusCode == http.StatusBadRequest
}

func (conn SplunkConnection) jobResultsURL(query *SplunkQuery, mode ResultEndpointMode) string {
	if mode == ResultEndpointV2 {
		return fmt.Sprintf("%s/services/search/v2/jobs/%s/results", conn.BaseURL, query.Job.Sid)
	}
	return fmt.Sprintf("%s/results/", conn.jobURL(query))
}

// Fetch job results.
func (conn SplunkConnection) jobResults(ctx context.Context, query *SplunkQuery, options DispatchOptions) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data = addParams(data, options.ResultParams)
	if data.Get("output_mode") == "" {
		data.Add("output_mode", "json")
	}

	mode := options.ResultEndpointMode
	if mode == ResultEndpointAuto {
		response, err := conn.httpGet(ctx, conn.jobResultsURL(query, ResultEndpointV2), &data)
		if err == nil {
			query.Results = []byte(response)
			return nil
		}
		if !canFallbackResultEndpoint(err) {
			query.Results = nil
			return err
		}
		log.Printf("INFO: Splunk search v2 results endpoint unavailable for job %s; falling back to v1 results endpoint", query.Job.Sid)
		mode = ResultEndpointV1
	}

	response, err := conn.httpGet(ctx, conn.jobResultsURL(query, mode), &data)
	if err != nil {
		query.Results = nil
		return err
	}

	query.Results = []byte(response)
	return nil
}

func (conn SplunkConnection) jobSearchLog(ctx context.Context, query *SplunkQuery) (string, error) {
	if query.Job.Sid == "" {
		return "", errors.New("cannot fetch search log without job sid")
	}

	data := make(url.Values)
	data = conn.namespaceValues(data)

	response, err := conn.httpGet(ctx, fmt.Sprintf("%s/search.log", conn.jobURL(query)), &data)
	if err != nil {
		return "", err
	}
	return response, nil
}

func AnalyzeJobLog(searchLog string) JobLogDiagnostics {
	diagnostics := JobLogDiagnostics{}
	for _, line := range strings.Split(searchLog, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if diagnostics.ExecutionDuration == "" {
			for _, pattern := range durationPatterns {
				matches := pattern.FindStringSubmatch(line)
				if len(matches) > 1 {
					diagnostics.ExecutionDuration = matches[1]
					break
				}
			}
		}

		if errorLogLevelPattern.MatchString(line) {
			diagnostics.Errors = appendBoundedLine(diagnostics.Errors, line)
			continue
		}
		if warnLogLevelPattern.MatchString(line) {
			diagnostics.Warnings = appendBoundedLine(diagnostics.Warnings, line)
		}
	}
	return diagnostics
}

func appendBoundedLine(lines []string, line string) []string {
	if len(lines) >= maxDiagnosticLines {
		return lines
	}
	if len(line) > maxDiagnosticLineLength {
		line = line[:maxDiagnosticLineLength] + "... [truncated]"
	}
	return append(lines, line)
}

func logDiagnostics(diagnostics JobLogDiagnostics) {
	if diagnostics.ExecutionDuration != "" {
		log.Printf("INFO: Splunk search execution duration: %s", diagnostics.ExecutionDuration)
	}
	for _, warning := range diagnostics.Warnings {
		log.Printf("WARN: Splunk search log warning: %s", warning)
	}
	for _, diagnosticError := range diagnostics.Errors {
		log.Printf("WARN: Splunk search log error: %s", diagnosticError)
	}
}

func shouldSummarizeSearchLog(mode SearchLogMode) bool {
	return mode == SearchLogModeSummary || mode == SearchLogModeBoth
}

func shouldSaveSearchLog(mode SearchLogMode) bool {
	return mode == SearchLogModeSave || mode == SearchLogModeBoth
}

func derivedSearchLogFile(outputFile string) string {
	if strings.TrimSpace(outputFile) == "" {
		return "splunk.search.log"
	}
	ext := filepath.Ext(outputFile)
	if ext == "" {
		return outputFile + ".search.log"
	}
	return strings.TrimSuffix(outputFile, ext) + ".search.log"
}

func (conn SplunkConnection) collectJobLogDiagnostics(ctx context.Context, query *SplunkQuery, options DispatchOptions) {
	if query.Job.Sid == "" {
		return
	}
	if options.SearchLogMode == SearchLogModeOff {
		return
	}

	searchLog, err := conn.jobSearchLog(ctx, query)
	if err != nil {
		log.Printf("WARN: could not fetch Splunk search log for job %s: %v", query.Job.Sid, err)
		return
	}
	query.SearchLogRead = true
	query.LogDiagnostics = AnalyzeJobLog(searchLog)
	if shouldSummarizeSearchLog(options.SearchLogMode) {
		logDiagnostics(query.LogDiagnostics)
	}
	if shouldSaveSearchLog(options.SearchLogMode) {
		searchLogFile := strings.TrimSpace(options.SearchLogFile)
		if searchLogFile == "" {
			searchLogFile = derivedSearchLogFile(options.OutputFile)
		}
		if err := os.WriteFile(searchLogFile, []byte(searchLog), 0644); err != nil {
			log.Printf("WARN: could not write Splunk search log for job %s to %s: %v", query.Job.Sid, searchLogFile, err)
			return
		}
		query.SearchLogFile = searchLogFile
		log.Printf("INFO: Wrote Splunk search log to %s", searchLogFile)
	}
}

// Dispatch Splunk Query: Main Entry Method.
func (conn SplunkConnection) DispatchQuery(ctx context.Context, query *SplunkQuery, outputfile string) error {
	return conn.DispatchQueryWithOptions(ctx, query, DefaultDispatchOptions(outputfile))
}

func (conn SplunkConnection) DispatchQueryWithOptions(ctx context.Context, query *SplunkQuery, options DispatchOptions) error {
	options = options.normalized()
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data = addParams(data, options.DispatchParams)
	data.Add("search", query.Query)

	response, err := conn.httpPost(ctx, fmt.Sprintf("%s/services/search/jobs/", conn.BaseURL), &data)
	if err != nil {
		return err
	}
	if strings.Contains(response, "Unauthorized") {
		return fmt.Errorf("%s", response)
	}

	if err = xml.Unmarshal([]byte(response), &query.Job); err != nil {
		return err
	}
	if query.Job.Sid == "" {
		return fmt.Errorf("empty job sid in response")
	}
	log.Printf("INFO: Dispatched Splunk search job %s", query.Job.Sid)

	if err = conn.jobStatus(ctx, query); err != nil {
		diagnosticCtx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
		defer cancel()
		conn.collectJobLogDiagnostics(diagnosticCtx, query, options)
		return err
	}
	if query.State != dispatchStateDone {
		conn.collectJobLogDiagnostics(ctx, query, options)
		return fmt.Errorf("unexpected job terminal state: %s", query.State)
	}
	conn.collectJobLogDiagnostics(ctx, query, options)

	if err = conn.jobResults(ctx, query, options); err != nil {
		return err
	}
	return conn.writeResults(query, options.OutputFile)
}

func (conn SplunkConnection) namespaceValues(values url.Values) url.Values {
	if values == nil {
		values = make(url.Values)
	}
	trimmedAppContext := strings.TrimSpace(conn.AppContext)
	if trimmedAppContext != "" {
		values.Add("namespace", trimmedAppContext)
	}
	return values
}
