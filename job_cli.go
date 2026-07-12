package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

const (
	jobActionStatus    = "status"
	jobActionWait      = "wait"
	jobActionResults   = "results"
	jobActionSearchLog = "search-log"
	jobActionCancel    = "cancel"
)

type jobCLIOptions struct {
	Action         string
	JobID          string
	OutputFile     string
	OutputExplicit bool
}

type jobFileSummary struct {
	Operation   string                   `json:"operation"`
	JobID       string                   `json:"sid"`
	OutputFile  string                   `json:"output_file"`
	Diagnostics splunk.JobLogDiagnostics `json:"diagnostics,omitempty"`
}

func validateJobMode(options jobCLIOptions, flagsSet map[string]bool, configFile, validateConfigFile, writeConfigFile string) (bool, error) {
	action := strings.TrimSpace(options.Action)
	jobID := strings.TrimSpace(options.JobID)
	active := action != "" || jobID != ""
	if !active {
		return false, nil
	}
	if action == "" || jobID == "" {
		return true, errors.New("-job-action and -job-sid must be provided together")
	}
	if err := splunk.ValidateJobID(options.JobID); err != nil {
		return true, err
	}
	switch action {
	case jobActionStatus, jobActionWait, jobActionResults, jobActionSearchLog, jobActionCancel:
	default:
		return true, fmt.Errorf("unsupported -job-action %q; use status, wait, results, search-log, or cancel", options.Action)
	}
	if strings.TrimSpace(configFile) != "" || strings.TrimSpace(validateConfigFile) != "" || strings.TrimSpace(writeConfigFile) != "" || flagsSet["q"] {
		return true, errors.New("job actions cannot be combined with query or YAML config modes")
	}
	for _, name := range []string{"earliest", "latest", "allow-old-earliest", "allow-index-wildcard", "force"} {
		if flagsSet[name] {
			return true, fmt.Errorf("-%s is not valid with job actions", name)
		}
	}
	if options.OutputExplicit && action != jobActionResults && action != jobActionSearchLog {
		return true, fmt.Errorf("-o is only valid with results or search-log job actions")
	}
	return true, nil
}

func runJobAction(ctx context.Context, client *splunk.Client, options jobCLIOptions, output io.Writer) error {
	switch strings.TrimSpace(options.Action) {
	case jobActionStatus:
		status, err := client.InspectJob(ctx, options.JobID)
		if err != nil {
			return err
		}
		return encodeJSON(output, status)
	case jobActionWait:
		status, err := client.WaitJob(ctx, options.JobID)
		if err != nil && strings.TrimSpace(status.State) == "" {
			return err
		}
		if encodeErr := encodeJSON(output, status); encodeErr != nil {
			return encodeErr
		}
		return err
	case jobActionResults:
		result, err := client.JobResultsToFile(ctx, options.JobID, splunk.JobResultsOptions{}, options.OutputFile)
		if err != nil {
			return err
		}
		return encodeJSON(output, jobFileSummary{Operation: jobActionResults, JobID: result.JobID, OutputFile: options.OutputFile})
	case jobActionSearchLog:
		if !options.OutputExplicit {
			jobLog, err := client.JobSearchLog(ctx, options.JobID)
			if err != nil {
				return err
			}
			_, err = io.WriteString(output, jobLog.Text)
			return err
		}
		jobLog, err := client.JobSearchLogToFile(ctx, options.JobID, options.OutputFile)
		if err != nil {
			return err
		}
		return encodeJSON(output, jobFileSummary{Operation: jobActionSearchLog, JobID: jobLog.JobID, OutputFile: options.OutputFile, Diagnostics: jobLog.Diagnostics})
	case jobActionCancel:
		result, err := client.CancelJob(ctx, options.JobID)
		if err != nil {
			return err
		}
		return encodeJSON(output, result)
	default:
		return fmt.Errorf("unsupported job action %q", options.Action)
	}
}

func encodeJSON(output io.Writer, value any) error {
	if output == nil {
		return errors.New("output writer is required")
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
