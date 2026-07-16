package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func TestJSONEventSinkWritesOneObjectPerLine(t *testing.T) {
	var output bytes.Buffer
	sink := newJSONEventSink(&output)
	sink.emitFinding("result_contains_credentials")
	sink.HandleEvent(context.Background(), splunk.RuntimeEvent{Sequence: 1, Kind: splunk.EventJobStatus, Severity: "info", JobID: "safe-job", State: "RUNNING"})
	sink.HandleEvent(context.Background(), splunk.RuntimeEvent{Sequence: 2, Kind: splunk.EventOperation, Severity: "info", Operation: "wait", Outcome: "success"})
	scanner := bufio.NewScanner(&output)
	count := 0
	for scanner.Scan() {
		count++
		var event splunk.RuntimeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("line %d is not JSON: %v", count, err)
		}
		if event.Sequence != uint64(count) {
			t.Fatalf("line %d sequence = %d", count, event.Sequence)
		}
		if event.Time.IsZero() {
			t.Fatalf("line %d has no timestamp", count)
		}
		if count == 1 && (event.Kind != splunk.EventFinding || event.FindingCode != "result_contains_credentials" || event.Severity != splunk.EventSeverityWarning) {
			t.Fatalf("unexpected finding event: %+v", event)
		}
	}
	if err := scanner.Err(); err != nil || count != 3 {
		t.Fatalf("lines=%d error=%v output=%q", count, err, output.String())
	}
}

func TestJSONEventsDoNotContaminateRawJobOutput(t *testing.T) {
	const jobID = "raw-log-job"
	const rawLog = "INFO raw search log\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/services/authentication/current-context":
			_, _ = io.WriteString(w, `{"entry":[{}]}`)
		case "/services/search/jobs/" + jobID + "/search.log":
			_, _ = io.WriteString(w, rawLog)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	var events, rawOutput bytes.Buffer
	client, err := splunk.NewClient(splunk.Config{BaseURL: server.URL, Token: "token", HTTPClient: server.Client(), PollInterval: time.Millisecond, EventSink: newJSONEventSink(&events)})
	if err != nil {
		t.Fatal(err)
	}
	if err := runJobAction(context.Background(), client, jobCLIOptions{Action: jobActionSearchLog, JobID: jobID}, &rawOutput); err != nil {
		t.Fatal(err)
	}
	if rawOutput.String() != rawLog {
		t.Fatalf("raw stdout changed: %q", rawOutput.String())
	}
	if events.Len() == 0 || bytes.Contains(rawOutput.Bytes(), []byte(`"kind"`)) {
		t.Fatalf("events=%q raw=%q", events.String(), rawOutput.String())
	}
}

func TestJSONEventsCoverEarlyCLIValidationErrors(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestJSONEventsCLIValidationHelper$")
	command.Env = append(os.Environ(), "QUERYSPLUNK_CLI_VALIDATION_HELPER=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 2 {
		t.Fatalf("exit error = %v, stderr = %q", err, stderr.String())
	}

	scanner := bufio.NewScanner(&stderr)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		var event splunk.RuntimeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("stderr line %d is not JSON: %v; output=%q", lineCount, err, stderr.String())
		}
		if event.Kind != splunk.EventOperation || event.Operation != "arguments" || event.Outcome != "failure" {
			t.Fatalf("unexpected event: %+v", event)
		}
	}
	if err := scanner.Err(); err != nil || lineCount != 1 {
		t.Fatalf("lines=%d error=%v output=%q", lineCount, err, stderr.String())
	}
}

func TestJSONEventsCLIValidationHelper(t *testing.T) {
	if os.Getenv("QUERYSPLUNK_CLI_VALIDATION_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("querysplunk", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = []string{"querysplunk", "-json-events", "-job-sid", "safe-job"}
	main()
}
