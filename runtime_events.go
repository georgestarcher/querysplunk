package main

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

type jsonEventSink struct {
	mu               sync.Mutex
	encoder          *json.Encoder
	lastSequence     uint64
	failedOperations map[string]bool
}

func newJSONEventSink(output io.Writer) *jsonEventSink {
	return &jsonEventSink{encoder: json.NewEncoder(output), failedOperations: make(map[string]bool)}
}

func (sink *jsonEventSink) HandleEvent(_ context.Context, event splunk.RuntimeEvent) {
	if sink == nil || sink.encoder == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if event.Sequence <= sink.lastSequence {
		sink.lastSequence++
		event.Sequence = sink.lastSequence
	} else {
		sink.lastSequence = event.Sequence
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if event.Kind == splunk.EventOperation && event.Outcome == "failure" {
		sink.failedOperations[event.Operation] = true
	}
	_ = sink.encoder.Encode(event)
}

func (sink *jsonEventSink) emitFinding(code string) {
	if sink == nil || sink.encoder == nil {
		return
	}
	sink.HandleEvent(context.Background(), splunk.RuntimeEvent{
		Kind:        splunk.EventFinding,
		Severity:    splunk.EventSeverityWarning,
		Operation:   "search",
		FindingCode: code,
	})
}

func (sink *jsonEventSink) ensureFailure(operation string) {
	if sink == nil || sink.encoder == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.failedOperations[operation] {
		return
	}
	sink.lastSequence++
	sink.failedOperations[operation] = true
	_ = sink.encoder.Encode(splunk.RuntimeEvent{Sequence: sink.lastSequence, Time: time.Now().UTC(), Kind: splunk.EventOperation, Severity: splunk.EventSeverityError, Operation: operation, Outcome: "failure"})
}

func reportCLIError(output io.Writer, sink *jsonEventSink, operation string, err error) {
	if sink != nil {
		sink.ensureFailure(operation)
		return
	}
	_, _ = io.WriteString(output, "ERROR: "+err.Error()+"\n")
}
