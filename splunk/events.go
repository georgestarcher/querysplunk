package splunk

import (
	"context"
	"sync"
	"time"
)

// RuntimeEventKind identifies a stable lifecycle event.
type RuntimeEventKind string

// RuntimeEventSeverity is the stable event severity vocabulary.
type RuntimeEventSeverity string

const (
	EventJobDispatched    RuntimeEventKind = "job_dispatched"
	EventJobStatus        RuntimeEventKind = "job_status"
	EventEndpointFallback RuntimeEventKind = "endpoint_fallback"
	EventDiagnostics      RuntimeEventKind = "diagnostics"
	EventCancellation     RuntimeEventKind = "cancellation"
	EventOutputSaved      RuntimeEventKind = "output_saved"
	EventOperation        RuntimeEventKind = "operation"
	EventFinding          RuntimeEventKind = "finding"
)

const (
	EventSeverityInfo    RuntimeEventSeverity = "info"
	EventSeverityWarning RuntimeEventSeverity = "warning"
	EventSeverityError   RuntimeEventSeverity = "error"
)

// RuntimeEvent is a non-sensitive immutable lifecycle snapshot. It never
// contains credentials, URLs, SPL, result bodies, raw search logs, or
// diagnostic lines. Zero-valued optional fields are omitted from JSON.
type RuntimeEvent struct {
	Sequence          uint64               `json:"sequence"`
	Time              time.Time            `json:"time"`
	Kind              RuntimeEventKind     `json:"kind"`
	Severity          RuntimeEventSeverity `json:"severity"`
	Operation         string               `json:"operation,omitempty"`
	JobID             string               `json:"sid,omitempty"`
	State             string               `json:"state,omitempty"`
	DoneProgress      float64              `json:"done_progress,omitempty"`
	ScanCount         int64                `json:"scan_count,omitempty"`
	EventCount        int64                `json:"event_count,omitempty"`
	ResultCount       int64                `json:"result_count,omitempty"`
	FromEndpoint      string               `json:"from_endpoint,omitempty"`
	ToEndpoint        string               `json:"to_endpoint,omitempty"`
	ExecutionDuration string               `json:"execution_duration,omitempty"`
	WarningCount      int                  `json:"warning_count,omitempty"`
	ErrorCount        int                  `json:"error_count,omitempty"`
	OutputFile        string               `json:"output_file,omitempty"`
	CancelRequested   bool                 `json:"cancel_requested,omitempty"`
	Outcome           string               `json:"outcome,omitempty"`
	FindingCode       string               `json:"finding_code,omitempty"`
}

// EventSink receives events synchronously in sequence order. Client serializes
// calls, including across concurrent searches. Implementations should return
// quickly and must not call back into the same Client.
type EventSink interface {
	HandleEvent(context.Context, RuntimeEvent)
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(context.Context, RuntimeEvent)

func (function EventSinkFunc) HandleEvent(ctx context.Context, event RuntimeEvent) {
	if function != nil {
		function(ctx, event)
	}
}

type eventDispatcher struct {
	mu       sync.Mutex
	sequence uint64
	sink     EventSink
}

func (dispatcher *eventDispatcher) emit(ctx context.Context, event RuntimeEvent) {
	if dispatcher == nil || dispatcher.sink == nil {
		return
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	dispatcher.sequence++
	event.Sequence = dispatcher.sequence
	event.Time = time.Now().UTC()
	dispatcher.sink.HandleEvent(ctx, event)
}
