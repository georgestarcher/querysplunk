package splunk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidJobID identifies an unsafe or empty Splunk search job ID.
	ErrInvalidJobID = errors.New("invalid splunk job ID")
	jobIDPattern    = regexp.MustCompile(`^[A-Za-z0-9._:@-]+$`)
)

// JobStatus is an immutable snapshot of a Splunk search job. Numeric fields
// are zero when Splunk omits them. Terminal includes both DONE and unsuccessful
// terminal states; Successful is true only for DONE.
type JobStatus struct {
	JobID        string  `json:"sid"`
	State        string  `json:"state"`
	DoneProgress float64 `json:"done_progress"`
	ScanCount    int64   `json:"scan_count"`
	EventCount   int64   `json:"event_count"`
	ResultCount  int64   `json:"result_count"`
	Terminal     bool    `json:"terminal"`
	Successful   bool    `json:"successful"`
}

// JobResultsOptions controls result retrieval for an existing job.
type JobResultsOptions struct {
	Params   map[string][]string
	Endpoint ResultEndpointMode
}

// JobLog contains an unmodified search.log response and bounded analysis.
type JobLog struct {
	JobID       string            `json:"sid"`
	Text        string            `json:"-"`
	Diagnostics JobLogDiagnostics `json:"diagnostics"`
}

// JobCancellation reports whether querysplunk submitted a cancel request. A
// terminal job is returned with Requested false and no control request.
type JobCancellation struct {
	Job       JobStatus `json:"job"`
	Requested bool      `json:"cancel_requested"`
}

// ValidateJobID rejects values that could alter a REST path. Splunk job IDs
// are opaque; accepted values are limited to the documented/common ASCII SID
// characters and are still path-escaped when used.
func ValidateJobID(jobID string) error {
	if jobID == "" || jobID != strings.TrimSpace(jobID) || len(jobID) > 512 {
		return fmt.Errorf("%w: SID must be 1 to 512 characters without surrounding whitespace", ErrInvalidJobID)
	}
	if strings.Contains(jobID, "..") || !jobIDPattern.MatchString(jobID) {
		return fmt.Errorf("%w: SID contains unsupported characters", ErrInvalidJobID)
	}
	return nil
}

// InspectJob returns one current status snapshot without waiting.
func (client *Client) InspectJob(ctx context.Context, jobID string) (JobStatus, error) {
	if err := ValidateJobID(jobID); err != nil {
		return JobStatus{}, err
	}
	conn, err := client.connection(ctx)
	if err != nil {
		return JobStatus{}, err
	}
	status, err := conn.inspectJob(ctx, jobID)
	if err != nil {
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventOperation, Severity: EventSeverityError, Operation: "inspect", JobID: jobID, Outcome: "failure"})
		return status, err
	}
	conn.emitEvent(ctx, statusEvent("inspect", status))
	return status, nil
}

// WaitJob waits for an existing job to reach a terminal state. Cancelling the
// local context never cancels the pre-existing remote job.
func (client *Client) WaitJob(ctx context.Context, jobID string) (JobStatus, error) {
	status, err := client.InspectJob(ctx, jobID)
	if err != nil {
		return status, err
	}
	if status.Terminal {
		status, err = completedJobStatus(status)
		client.emitRuntime(ctx, operationEvent("wait", status.JobID, status.State, err))
		return status, err
	}

	client.mu.Lock()
	interval := client.conn.pollInterval()
	client.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			client.emitRuntime(ctx, RuntimeEvent{Kind: EventOperation, Severity: EventSeverityWarning, Operation: "wait", JobID: jobID, State: status.State, Outcome: "cancelled"})
			return status, ctx.Err()
		case <-ticker.C:
		}
		nextStatus, err := client.InspectJob(ctx, jobID)
		if err != nil {
			client.emitRuntime(ctx, operationEvent("wait", jobID, status.State, err))
			return status, err
		}
		status = nextStatus
		if status.Terminal {
			status, err = completedJobStatus(status)
			client.emitRuntime(ctx, operationEvent("wait", jobID, status.State, err))
			return status, err
		}
	}
}

// JobResults buffers the unmodified results response for an existing job.
func (client *Client) JobResults(ctx context.Context, jobID string, options JobResultsOptions) (Result, error) {
	var output bytes.Buffer
	result, err := client.JobResultsTo(ctx, jobID, options, &output)
	if err != nil {
		return result, err
	}
	result.Data = bytes.Clone(output.Bytes())
	return result, nil
}

// JobResultsTo streams the unmodified results response for an existing job.
func (client *Client) JobResultsTo(ctx context.Context, jobID string, options JobResultsOptions, output io.Writer) (Result, error) {
	if err := ValidateJobID(jobID); err != nil {
		return Result{}, err
	}
	if output == nil {
		return Result{}, fmt.Errorf("%w: output writer is required", ErrInvalidConfig)
	}
	if err := validateJobResultsOptions(options); err != nil {
		return Result{}, err
	}
	conn, err := client.connection(ctx)
	if err != nil {
		return Result{}, err
	}
	query := queryState{Job: splunkJob{Sid: jobID}}
	dispatch := dispatchOptions{ResultParams: cloneParams(options.Params), ResultEndpointMode: options.Endpoint}.normalized()
	if err := conn.jobResultsToWriter(ctx, &query, dispatch, output); err != nil {
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventOperation, Severity: EventSeverityError, Operation: "results", JobID: jobID, Outcome: "failure"})
		return resultFromQuery(query, nil), err
	}
	query.State = dispatchStateDone
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventOperation, Severity: EventSeverityInfo, Operation: "results", JobID: jobID, State: query.State, Outcome: "success"})
	return resultFromQuery(query, nil), nil
}

// JobResultsToFile atomically replaces path only after result retrieval and
// local writes succeed.
func (client *Client) JobResultsToFile(ctx context.Context, jobID string, options JobResultsOptions, path string) (Result, error) {
	if err := ValidateJobID(jobID); err != nil {
		return Result{}, err
	}
	if err := validateJobResultsOptions(options); err != nil {
		return Result{}, err
	}
	if err := validateOutputPath(path); err != nil {
		return Result{}, err
	}
	var result Result
	err := writeAtomically(path, func(output io.Writer) error {
		var err error
		result, err = client.JobResultsTo(ctx, jobID, options, output)
		return err
	})
	if err == nil {
		client.emitRuntime(ctx, RuntimeEvent{Kind: EventOutputSaved, Severity: EventSeverityInfo, Operation: "results", JobID: jobID, OutputFile: path, Outcome: "success"})
	}
	return result, err
}

// JobSearchLog fetches the raw search.log and returns bounded diagnostics.
func (client *Client) JobSearchLog(ctx context.Context, jobID string) (JobLog, error) {
	if err := ValidateJobID(jobID); err != nil {
		return JobLog{}, err
	}
	conn, err := client.connection(ctx)
	if err != nil {
		return JobLog{}, err
	}
	query := queryState{Job: splunkJob{Sid: jobID}}
	text, err := conn.jobSearchLog(ctx, &query)
	if err != nil {
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventOperation, Severity: EventSeverityError, Operation: "search_log", JobID: jobID, Outcome: "failure"})
		return JobLog{JobID: jobID}, err
	}
	diagnostics := AnalyzeJobLog(text)
	severity := EventSeverityInfo
	if len(diagnostics.Warnings) > 0 {
		severity = EventSeverityWarning
	}
	if len(diagnostics.Errors) > 0 {
		severity = EventSeverityError
	}
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventDiagnostics, Severity: severity, Operation: "search_log", JobID: jobID, ExecutionDuration: diagnostics.ExecutionDuration, WarningCount: len(diagnostics.Warnings), ErrorCount: len(diagnostics.Errors)})
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventOperation, Severity: EventSeverityInfo, Operation: "search_log", JobID: jobID, Outcome: "success"})
	return JobLog{JobID: jobID, Text: text, Diagnostics: diagnostics}, nil
}

// JobSearchLogToFile atomically writes an unmodified search.log response.
func (client *Client) JobSearchLogToFile(ctx context.Context, jobID, path string) (JobLog, error) {
	if err := ValidateJobID(jobID); err != nil {
		return JobLog{}, err
	}
	if err := validateOutputPath(path); err != nil {
		return JobLog{JobID: jobID}, err
	}
	jobLog, err := client.JobSearchLog(ctx, jobID)
	if err != nil {
		return jobLog, err
	}
	err = writeAtomically(path, func(output io.Writer) error {
		_, err := io.WriteString(output, jobLog.Text)
		return err
	})
	if err == nil {
		client.emitRuntime(ctx, RuntimeEvent{Kind: EventOutputSaved, Severity: EventSeverityInfo, Operation: "search_log", JobID: jobID, OutputFile: path, Outcome: "success"})
	}
	return jobLog, err
}

// CancelJob explicitly requests cancellation. It is idempotent for terminal
// jobs, which are returned without posting a control action.
func (client *Client) CancelJob(ctx context.Context, jobID string) (JobCancellation, error) {
	status, err := client.InspectJob(ctx, jobID)
	if err != nil {
		return JobCancellation{}, err
	}
	result := JobCancellation{Job: status}
	if status.Terminal {
		client.emitRuntime(ctx, RuntimeEvent{Kind: EventCancellation, Severity: EventSeverityInfo, Operation: "cancel", JobID: jobID, State: status.State, Outcome: "already_terminal"})
		return result, nil
	}
	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()
	query := queryState{Job: splunkJob{Sid: jobID}}
	if err := conn.cancelJobContext(ctx, &query); err != nil {
		latest, inspectErr := client.InspectJob(ctx, jobID)
		if inspectErr == nil && latest.Terminal {
			result.Job = latest
			conn.emitEvent(ctx, RuntimeEvent{Kind: EventCancellation, Severity: EventSeverityInfo, Operation: "cancel", JobID: jobID, State: latest.State, Outcome: "already_terminal"})
			return result, nil
		}
		conn.emitEvent(ctx, RuntimeEvent{Kind: EventCancellation, Severity: EventSeverityError, Operation: "cancel", JobID: jobID, State: status.State, Outcome: "failure"})
		return result, err
	}
	result.Requested = true
	conn.emitEvent(ctx, RuntimeEvent{Kind: EventCancellation, Severity: EventSeverityInfo, Operation: "cancel", JobID: jobID, State: status.State, CancelRequested: true, Outcome: "requested"})
	return result, nil
}

func (client *Client) emitRuntime(ctx context.Context, event RuntimeEvent) {
	if client == nil {
		return
	}
	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()
	conn.emitEvent(ctx, event)
}

func statusEvent(operation string, status JobStatus) RuntimeEvent {
	return RuntimeEvent{Kind: EventJobStatus, Severity: EventSeverityInfo, Operation: operation, JobID: status.JobID, State: status.State, DoneProgress: status.DoneProgress, ScanCount: status.ScanCount, EventCount: status.EventCount, ResultCount: status.ResultCount}
}

func operationEvent(operation, jobID, state string, err error) RuntimeEvent {
	severity, outcome := EventSeverityInfo, "success"
	if err != nil {
		severity, outcome = EventSeverityError, "failure"
	}
	return RuntimeEvent{Kind: EventOperation, Severity: severity, Operation: operation, JobID: jobID, State: state, Outcome: outcome}
}

func (client *Client) connection(ctx context.Context) (connection, error) {
	if client == nil {
		return connection{}, fmt.Errorf("%w: nil Client", ErrInvalidConfig)
	}
	if err := client.Authenticate(ctx); err != nil {
		return connection{}, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.conn, nil
}

func (conn connection) inspectJob(ctx context.Context, jobID string) (JobStatus, error) {
	query := queryState{Job: splunkJob{Sid: jobID}}
	data := conn.namespaceValues(make(url.Values))
	data.Add("output_mode", "json")
	response, err := conn.httpGet(ctx, conn.jobURL(&query), &data)
	if err != nil {
		return JobStatus{JobID: jobID}, err
	}
	content, err := parseJobStatus(response)
	if err != nil {
		return JobStatus{JobID: jobID}, err
	}
	if strings.TrimSpace(content.DispatchState) == "" {
		return JobStatus{JobID: jobID}, errors.New("splunk job status response did not include dispatchState")
	}
	return publicJobStatus(jobID, content), nil
}

func publicJobStatus(jobID string, content jobStatusContent) JobStatus {
	state := strings.ToUpper(strings.TrimSpace(content.DispatchState))
	terminal := state == dispatchStateDone || isTerminalErrorState(state)
	if state == dispatchStatePause || state == dispatchStatePaused {
		terminal = false
	}
	return JobStatus{
		JobID:        jobID,
		State:        state,
		DoneProgress: parseFloat(content.DoneProgress),
		ScanCount:    parseCount(content.ScanCount),
		EventCount:   parseCount(content.EventCount),
		ResultCount:  parseCount(content.ResultCount),
		Terminal:     terminal,
		Successful:   state == dispatchStateDone,
	}
}

func completedJobStatus(status JobStatus) (JobStatus, error) {
	if status.Successful {
		return status, nil
	}
	return status, &JobStateError{State: status.State}
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func parseCount(value string) int64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return int64(parsed)
}

func validateJobResultsOptions(options JobResultsOptions) error {
	if options.Endpoint != "" && options.Endpoint != ResultEndpointAuto && options.Endpoint != ResultEndpointV1 && options.Endpoint != ResultEndpointV2 {
		return fmt.Errorf("%w: unsupported result endpoint %q", ErrInvalidConfig, options.Endpoint)
	}
	for key := range options.Params {
		if strings.EqualFold(strings.TrimSpace(key), "namespace") {
			return fmt.Errorf("%w: namespace is controlled by Client Config.App", ErrInvalidConfig)
		}
	}
	return nil
}

func (conn connection) cancelJobContext(ctx context.Context, query *queryState) error {
	data := conn.namespaceValues(make(url.Values))
	data.Add("action", "cancel")
	_, err := conn.httpPost(ctx, fmt.Sprintf("%s/control", conn.jobURL(query)), &data)
	return err
}

func writeAtomically(path string, write func(io.Writer) error) error {
	if err := validateOutputPath(path); err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	temporary, err := os.CreateTemp(filepath.Dir(path), ".querysplunk-job-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := write(temporary); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func validateOutputPath(path string) error {
	if path == "" || strings.TrimSpace(path) == "" || path != strings.TrimSpace(path) {
		return fmt.Errorf("%w: output path is required without surrounding whitespace", ErrInvalidConfig)
	}
	return nil
}
