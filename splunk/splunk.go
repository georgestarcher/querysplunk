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
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Data Structures

// connection contains legacy mutable connection state.
// Deprecated: use Client and Config.
type connection struct {
	Username, Password, BaseURL string
	AppContext                  string
	sessionKey                  sessionKey
	AuthToken                   string
	TLSVerify                   bool
	Timeout                     time.Duration
	PollInterval                time.Duration
	client                      *http.Client
	logger                      *slog.Logger
	events                      *eventDispatcher
}

// sessionKey is a legacy decoded login response.
// Deprecated: Client manages session keys internally.
type sessionKey struct {
	Value string `json:"sessionKey"`
}

// splunkJob is a legacy decoded dispatch response.
// Deprecated: use Result.JobID.
type splunkJob struct {
	XMLName xml.Name `xml:"response"`
	Text    string   `xml:",chardata"`
	Sid     string   `xml:"sid"`
}

// queryState contains legacy mutable search state.
// Deprecated: use Client.Search and Result.
type queryState struct {
	Query          string
	Job            splunkJob
	State          string
	Results        []byte
	LogDiagnostics JobLogDiagnostics
	SearchLogRead  bool
	SearchLogFile  string
}

// splunkJobStatus is a legacy decoded status response.
// Deprecated: job status parsing is an implementation detail.
type splunkJobStatus struct {
	Entry []struct {
		Content map[string]any `json:"content"`
	} `json:"entry"`
}

// JobLogDiagnostics contains bounded diagnostics extracted from search.log.
type JobLogDiagnostics struct {
	ExecutionDuration string   `json:"execution_duration,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	Errors            []string `json:"errors,omitempty"`
}

// HTTPStatusError reports a non-2xx Splunk REST response. URL excludes user
// information and query parameters; Body is bounded.
type HTTPStatusError struct {
	StatusCode int
	Status     string
	URL        string
	Body       string
}

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("request failed with status %s for %s: %s", err.Status, err.URL, err.Body)
}

// SearchLogMode controls whether search.log is ignored, summarized, or saved.
type SearchLogMode string

const (
	SearchLogModeOff     SearchLogMode = "off"
	SearchLogModeSummary SearchLogMode = "summary"
	SearchLogModeSave    SearchLogMode = "save"
	SearchLogModeBoth    SearchLogMode = "both"
)

// ResultEndpointMode selects Splunk's v1 or v2 result endpoint.
type ResultEndpointMode string

const (
	ResultEndpointAuto ResultEndpointMode = "auto"
	ResultEndpointV1   ResultEndpointMode = "v1"
	ResultEndpointV2   ResultEndpointMode = "v2"
)

// ExecutionMode selects a polled job or direct export request.
type ExecutionMode string

const (
	ExecutionModeJob    ExecutionMode = "job"
	ExecutionModeExport ExecutionMode = "export"
)

// dispatchOptions controls the legacy mutable dispatch API.
// Deprecated: use SearchOptions.
type dispatchOptions struct {
	OutputFile         string
	DispatchParams     map[string][]string
	ResultParams       map[string][]string
	ResultEndpointMode ResultEndpointMode
	ExecutionMode      ExecutionMode
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
	maxErrorBodyBytes           = 2048
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

func (conn connection) httpClient() *http.Client {
	if conn.client != nil {
		return conn.client
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !conn.TLSVerify},
	}
	client := &http.Client{Transport: tr, Timeout: conn.Timeout}
	return client
}

func (conn connection) httpGet(ctx context.Context, url string, data *url.Values) (string, error) {
	return conn.httpCall(ctx, url, http.MethodGet, data)
}

func (conn connection) httpPost(ctx context.Context, url string, data *url.Values) (string, error) {
	return conn.httpCall(ctx, url, http.MethodPost, data)
}

func (conn connection) httpCallToWriter(ctx context.Context, requestURL string, method string, data *url.Values, output io.Writer) error {
	client := conn.httpClient()
	var payload io.Reader
	if method == http.MethodGet && data != nil {
		parsedURL, err := url.Parse(requestURL)
		if err != nil {
			return err
		}
		query := parsedURL.Query()
		for key, values := range *data {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsedURL.RawQuery = query.Encode()
		requestURL = parsedURL.String()
	} else if data != nil {
		payload = bytes.NewBufferString(data.Encode())
	}

	request, err := http.NewRequestWithContext(ctx, method, requestURL, payload)
	if err != nil {
		return err
	}
	if method == http.MethodPost && data != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	conn.addAuthHeader(request)

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
		if readErr != nil {
			return readErr
		}
		return &HTTPStatusError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			URL:        safeURLForLog(requestURL),
			Body:       responseBodyForError(body),
		}
	}

	_, err = io.Copy(output, response.Body)
	return err
}

func (conn connection) httpCall(ctx context.Context, requestURL string, method string, data *url.Values) (string, error) {
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

func (conn connection) addAuthHeader(request *http.Request) {
	// use auth token first if provided. then session key if already obtained. login with credentials last
	if conn.AuthToken != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", conn.AuthToken))
	} else if conn.sessionKey.Value != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Splunk %s", conn.sessionKey.Value))
	} else {
		request.SetBasicAuth(conn.Username, conn.Password)
	}
}

// Splunk Methods

// Login validates a token or retrieves a session key.
// Deprecated: use Client.Authenticate.
func (conn *connection) login(ctx context.Context) error {
	if conn.AuthToken != "" {
		return conn.validateAuth(ctx)
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

	var key sessionKey
	if err := json.Unmarshal([]byte(response), &key); err != nil {
		return err
	}
	if key.Value == "" {
		return errors.New("could not parse sessionKey from login response")
	}
	conn.sessionKey = key
	return nil
}

// ValidateAuth checks whether authentication can access the Splunk REST API.
// Deprecated: use Client.Authenticate.
func (conn connection) validateAuth(ctx context.Context) error {
	data := make(url.Values)
	data.Add("output_mode", "json")
	_, err := conn.httpGet(ctx, fmt.Sprintf("%s/services/authentication/current-context", conn.BaseURL), &data)
	if err != nil {
		return fmt.Errorf("validate splunk authentication: %w", err)
	}
	return nil
}

// Return URL string formatted with job sid.
func (conn connection) jobURL(query *queryState) string {
	return fmt.Sprintf("%s/services/search/jobs/%s", conn.BaseURL, url.PathEscape(query.Job.Sid))
}

func (conn connection) pollInterval() time.Duration {
	if conn.PollInterval > 0 {
		return conn.PollInterval
	}
	return defaultPollInterval
}

func (conn connection) logInfo(ctx context.Context, message string, args ...any) {
	if conn.logger != nil {
		conn.logger.InfoContext(ctx, message, args...)
	}
}

func (conn connection) logWarn(ctx context.Context, message string, args ...any) {
	if conn.logger != nil {
		conn.logger.WarnContext(ctx, message, args...)
	}
}

func (conn connection) emitEvent(ctx context.Context, event RuntimeEvent) {
	conn.events.emit(ctx, event)
}

// Check on job status until terminal state or context deadline.
func (conn connection) jobStatus(ctx context.Context, query *queryState) error {
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
				cancelErr := conn.cancelJob(query)
				if cancelErr != nil {
					conn.logWarn(context.Background(), "Splunk search job cancellation failed", "job_id", query.Job.Sid)
				}
				outcome := "requested"
				if cancelErr != nil {
					outcome = "failed"
				}
				conn.emitEvent(context.Background(), RuntimeEvent{Kind: EventCancellation, Severity: EventSeverityWarning, Operation: "search", JobID: query.Job.Sid, State: query.State, CancelRequested: cancelErr == nil, Outcome: outcome})
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
			conn.logJobProgress(ctx, query.Job.Sid, status)
			lastLoggedState = query.State
		}
		if query.State == dispatchStateDone {
			return nil
		}
		if isTerminalErrorState(query.State) {
			return &JobStateError{State: query.State}
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
	var payload splunkJobStatus
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

func (conn connection) logJobProgress(ctx context.Context, sid string, status jobStatusContent) {
	args := []any{"job_id", sid, "state", status.DispatchState}
	if status.DoneProgress != "" {
		args = append(args, "done_progress", status.DoneProgress)
	}
	if status.ScanCount != "" {
		args = append(args, "scan_count", status.ScanCount)
	}
	if status.EventCount != "" {
		args = append(args, "event_count", status.EventCount)
	}
	if status.ResultCount != "" {
		args = append(args, "result_count", status.ResultCount)
	}
	conn.logInfo(ctx, "Splunk search job state changed", args...)
	public := publicJobStatus(sid, status)
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventJobStatus, Severity: EventSeverityInfo, Operation: "search", JobID: sid, State: public.State, DoneProgress: public.DoneProgress, ScanCount: public.ScanCount, EventCount: public.EventCount, ResultCount: public.ResultCount})
}

func (conn connection) cancelJob(query *queryState) error {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()

	return conn.cancelJobContext(ctx, query)
}

// defaultdispatchOptions returns legacy job-mode defaults.
// Deprecated: use SearchOptions.
func defaultdispatchOptions(outputFile string) dispatchOptions {
	return dispatchOptions{
		OutputFile:         outputFile,
		ResultEndpointMode: ResultEndpointAuto,
		ExecutionMode:      ExecutionModeJob,
		SearchLogMode:      SearchLogModeSummary,
	}
}

func (options dispatchOptions) normalized() dispatchOptions {
	if options.SearchLogMode == "" {
		options.SearchLogMode = SearchLogModeSummary
	}
	if options.ResultEndpointMode == "" {
		options.ResultEndpointMode = ResultEndpointAuto
	}
	if options.ExecutionMode == "" {
		options.ExecutionMode = ExecutionModeJob
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
func (conn connection) writeResults(query *queryState, outputfile string) error {
	return os.WriteFile(outputfile, query.Results, 0644)
}

func canFallbackResultEndpoint(err error) bool {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusMethodNotAllowed || statusErr.StatusCode == http.StatusBadRequest
}

func (conn connection) jobResultsURL(query *queryState, mode ResultEndpointMode) string {
	if mode == ResultEndpointV2 {
		return fmt.Sprintf("%s/services/search/v2/jobs/%s/results", conn.BaseURL, query.Job.Sid)
	}
	return fmt.Sprintf("%s/results/", conn.jobURL(query))
}

func (conn connection) exportURL(mode ResultEndpointMode) string {
	if mode == ResultEndpointV2 {
		return fmt.Sprintf("%s/services/search/v2/jobs/export", conn.BaseURL)
	}
	return fmt.Sprintf("%s/services/search/jobs/export", conn.BaseURL)
}

// Fetch job results.
func (conn connection) jobResults(ctx context.Context, query *queryState, options dispatchOptions) error {
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
		conn.logInfo(ctx, "Splunk search endpoint fallback", "job_id", query.Job.Sid, "operation", "results", "from_endpoint", "v2", "to_endpoint", "v1")
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventEndpointFallback, Severity: EventSeverityInfo, Operation: "results", JobID: query.Job.Sid, FromEndpoint: "v2", ToEndpoint: "v1"})
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

func (conn connection) jobResultsToWriter(ctx context.Context, query *queryState, options dispatchOptions, output io.Writer) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data = addParams(data, options.ResultParams)
	if data.Get("output_mode") == "" {
		data.Add("output_mode", "json")
	}

	mode := options.ResultEndpointMode
	if mode == ResultEndpointAuto {
		err := conn.httpCallToWriter(ctx, conn.jobResultsURL(query, ResultEndpointV2), http.MethodGet, &data, output)
		if err == nil {
			return nil
		}
		if !canFallbackResultEndpoint(err) {
			return err
		}
		conn.logInfo(ctx, "Splunk search endpoint fallback", "job_id", query.Job.Sid, "operation", "results", "from_endpoint", "v2", "to_endpoint", "v1")
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventEndpointFallback, Severity: EventSeverityInfo, Operation: "results", JobID: query.Job.Sid, FromEndpoint: "v2", ToEndpoint: "v1"})
		mode = ResultEndpointV1
	}
	return conn.httpCallToWriter(ctx, conn.jobResultsURL(query, mode), http.MethodGet, &data, output)
}

func (conn connection) exportQuery(ctx context.Context, query *queryState, options dispatchOptions, output io.Writer) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data = addParams(data, options.DispatchParams)
	data = addParams(data, options.ResultParams)
	if data.Get("output_mode") == "" {
		data.Add("output_mode", "json")
	}
	data.Add("search", query.Query)

	mode := options.ResultEndpointMode
	query.State = "EXPORT"
	if mode == ResultEndpointAuto {
		err := conn.httpCallToWriter(ctx, conn.exportURL(ResultEndpointV2), http.MethodPost, &data, output)
		if err == nil {
			query.State = dispatchStateDone
			return nil
		}
		if !canFallbackResultEndpoint(err) {
			query.Results = nil
			return err
		}
		conn.logInfo(ctx, "Splunk search endpoint fallback", "operation", "export", "from_endpoint", "v2", "to_endpoint", "v1")
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventEndpointFallback, Severity: EventSeverityInfo, Operation: "export", FromEndpoint: "v2", ToEndpoint: "v1"})
		mode = ResultEndpointV1
	}

	if err := conn.httpCallToWriter(ctx, conn.exportURL(mode), http.MethodPost, &data, output); err != nil {
		query.Results = nil
		return err
	}
	query.State = dispatchStateDone
	return nil
}

func (conn connection) jobSearchLog(ctx context.Context, query *queryState) (string, error) {
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

// AnalyzeJobLog extracts duration, warning, and error diagnostics from a
// Splunk search.log response. Diagnostic counts and line lengths are bounded.
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

func (conn connection) logDiagnostics(ctx context.Context, sid string, diagnostics JobLogDiagnostics) {
	if diagnostics.ExecutionDuration != "" {
		conn.logInfo(ctx, "Splunk search execution duration detected", "job_id", sid, "execution_duration", diagnostics.ExecutionDuration)
	}
	if len(diagnostics.Warnings) > 0 {
		conn.logWarn(ctx, "Splunk search log diagnostics detected", "job_id", sid, "severity", "warning", "count", len(diagnostics.Warnings))
	}
	if len(diagnostics.Errors) > 0 {
		conn.logWarn(ctx, "Splunk search log diagnostics detected", "job_id", sid, "severity", "error", "count", len(diagnostics.Errors))
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

func (conn connection) collectJobLogDiagnostics(ctx context.Context, query *queryState, options dispatchOptions) {
	if query.Job.Sid == "" {
		return
	}
	if options.SearchLogMode == SearchLogModeOff {
		return
	}

	searchLog, err := conn.jobSearchLog(ctx, query)
	if err != nil {
		conn.logWarn(ctx, "Splunk search log fetch failed", "job_id", query.Job.Sid)
		return
	}
	query.SearchLogRead = true
	query.LogDiagnostics = AnalyzeJobLog(searchLog)
	severity := EventSeverityInfo
	if len(query.LogDiagnostics.Warnings) > 0 {
		severity = EventSeverityWarning
	}
	if len(query.LogDiagnostics.Errors) > 0 {
		severity = EventSeverityError
	}
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventDiagnostics, Severity: severity, Operation: "search_log", JobID: query.Job.Sid, ExecutionDuration: query.LogDiagnostics.ExecutionDuration, WarningCount: len(query.LogDiagnostics.Warnings), ErrorCount: len(query.LogDiagnostics.Errors)})
	if shouldSummarizeSearchLog(options.SearchLogMode) {
		conn.logDiagnostics(ctx, query.Job.Sid, query.LogDiagnostics)
	}
	if shouldSaveSearchLog(options.SearchLogMode) {
		searchLogFile := strings.TrimSpace(options.SearchLogFile)
		if searchLogFile == "" {
			searchLogFile = derivedSearchLogFile(options.OutputFile)
		}
		if err := os.WriteFile(searchLogFile, []byte(searchLog), 0644); err != nil {
			conn.logWarn(ctx, "Splunk search log write failed", "job_id", query.Job.Sid)
			return
		}
		query.SearchLogFile = searchLogFile
		conn.logInfo(ctx, "Splunk search log saved", "job_id", query.Job.Sid)
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventOutputSaved, Severity: EventSeverityInfo, Operation: "search_log", JobID: query.Job.Sid, OutputFile: searchLogFile, Outcome: "success"})
	}
}

// dispatchQueryToFile executes a legacy mutable query and writes its response.
// Deprecated: use Client.Search.
func (conn connection) dispatchQueryToFile(ctx context.Context, query *queryState, outputfile string) error {
	return conn.dispatchQueryWithOptions(ctx, query, defaultdispatchOptions(outputfile))
}

// dispatchQueryWithOptions executes a legacy mutable query with options.
// Deprecated: use Client.Search.
func (conn connection) dispatchQueryWithOptions(ctx context.Context, query *queryState, options dispatchOptions) error {
	return conn.dispatchQuery(ctx, query, options, nil)
}

func (conn connection) dispatchQuery(ctx context.Context, query *queryState, options dispatchOptions, output io.Writer) error {
	options = options.normalized()
	if options.ExecutionMode == ExecutionModeExport {
		if output != nil {
			return conn.exportQuery(ctx, query, options, output)
		}
		return conn.httpPostToFileExport(ctx, query, options)
	}

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
	conn.logInfo(ctx, "Splunk search job dispatched", "job_id", query.Job.Sid)
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventJobDispatched, Severity: EventSeverityInfo, Operation: "search", JobID: query.Job.Sid, State: "DISPATCHED"})

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

	if output != nil {
		return conn.jobResultsToWriter(ctx, query, options, output)
	}
	if err = conn.jobResults(ctx, query, options); err != nil {
		return err
	}
	return conn.writeResults(query, options.OutputFile)
}

func (conn connection) httpPostToFileExport(ctx context.Context, query *queryState, options dispatchOptions) error {
	directory := filepath.Dir(options.OutputFile)
	temporary, err := os.CreateTemp(directory, ".querysplunk-export-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := conn.exportQuery(ctx, query, options, temporary); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		_ = temporary.Close()
		return err
	}

	output, err := os.Create(options.OutputFile)
	if err != nil {
		_ = temporary.Close()
		return err
	}
	_, copyErr := io.Copy(output, temporary)
	closeOutputErr := output.Close()
	closeTemporaryErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeOutputErr != nil {
		return closeOutputErr
	}
	return closeTemporaryErr
}

func (conn connection) namespaceValues(values url.Values) url.Values {
	if values == nil {
		values = make(url.Values)
	}
	trimmedAppContext := strings.TrimSpace(conn.AppContext)
	if trimmedAppContext != "" {
		values.Add("namespace", trimmedAppContext)
	}
	return values
}
