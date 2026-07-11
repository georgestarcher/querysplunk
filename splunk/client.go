package splunk

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	// ErrInvalidConfig identifies invalid client or search options.
	ErrInvalidConfig = errors.New("invalid splunk configuration")
	// ErrEmptySearch identifies a search containing no SPL.
	ErrEmptySearch = errors.New("splunk search is empty")
)

// JobStateError reports an unsuccessful terminal Splunk job state.
// Use errors.As to inspect State.
type JobStateError struct {
	State string
}

// Error implements error.
func (err *JobStateError) Error() string {
	return fmt.Sprintf("splunk job ended in %s state", err.State)
}

// Config configures a Client. Token authentication takes precedence over
// Username and Password. TLS certificates are verified unless
// InsecureSkipVerify is explicitly set. HTTPClient may be supplied for testing
// or a custom transport; when set, Timeout and InsecureSkipVerify must remain
// zero because the caller owns those policies and the HTTP client. Logger is
// optional; when nil, the package emits no logs. A supplied logger may be used
// concurrently and receives only bounded, non-sensitive operational fields.
type Config struct {
	BaseURL            string
	Token              string
	Username           string
	Password           string
	App                string
	Timeout            time.Duration
	PollInterval       time.Duration
	InsecureSkipVerify bool
	HTTPClient         *http.Client
	Logger             *slog.Logger
}

// Client is a Splunk REST client. Its zero value is not usable; construct one
// with NewClient. A Client may be used concurrently. Close releases idle
// connections owned by the client and leaves a caller-supplied HTTPClient open.
type Client struct {
	mu            sync.Mutex
	conn          connection
	authenticated bool
	ownedClient   *http.Client
}

// SearchOptions controls dispatch, result retrieval, and search.log handling.
// Parameter maps are copied before use. SearchLogFile is used only for save or
// both mode.
type SearchOptions struct {
	DispatchParams map[string][]string
	ResultParams   map[string][]string
	ResultEndpoint ResultEndpointMode
	ExecutionMode  ExecutionMode
	SearchLog      SearchLogMode
	SearchLogFile  string
}

// Result contains one completed response and non-secret job provenance. Data
// and Diagnostics are owned by the caller and are independent for each call.
type Result struct {
	Data          []byte
	JobID         string
	State         string
	Diagnostics   JobLogDiagnostics
	SearchLogRead bool
	SearchLogFile string
}

// NewClient validates config and constructs a secure-by-default client. It
// performs no network I/O; call Authenticate to validate access eagerly.
func NewClient(config Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("%w: BaseURL must be an absolute HTTP(S) URL", ErrInvalidConfig)
	}
	if config.Token == "" && (config.Username == "" || config.Password == "") {
		return nil, fmt.Errorf("%w: provide Token or both Username and Password", ErrInvalidConfig)
	}
	if config.Timeout < 0 || config.PollInterval < 0 {
		return nil, fmt.Errorf("%w: timeout values cannot be negative", ErrInvalidConfig)
	}
	if config.HTTPClient != nil && (config.Timeout != 0 || config.InsecureSkipVerify) {
		return nil, fmt.Errorf("%w: HTTPClient cannot be combined with Timeout or InsecureSkipVerify", ErrInvalidConfig)
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	var httpClient *http.Client
	var ownedClient *http.Client
	if config.HTTPClient != nil {
		httpClient = config.HTTPClient
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
		transport.TLSClientConfig.InsecureSkipVerify = config.InsecureSkipVerify
		httpClient = &http.Client{Transport: transport, Timeout: timeout}
		ownedClient = httpClient
	}

	return &Client{
		conn: connection{
			Username:     config.Username,
			Password:     config.Password,
			BaseURL:      baseURL,
			AppContext:   strings.TrimSpace(config.App),
			AuthToken:    config.Token,
			TLSVerify:    !config.InsecureSkipVerify,
			Timeout:      timeout,
			PollInterval: config.PollInterval,
			client:       httpClient,
			logger:       config.Logger,
		},
		ownedClient: ownedClient,
	}, nil
}

// Authenticate validates token access or obtains a session key. Repeated calls
// return immediately after the first success. Context cancellation remains
// discoverable with errors.Is.
func (client *Client) Authenticate(ctx context.Context) error {
	if client == nil {
		return fmt.Errorf("%w: nil Client", ErrInvalidConfig)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.authenticated {
		return nil
	}
	if err := client.conn.login(ctx); err != nil {
		return fmt.Errorf("authenticate with Splunk: %w", err)
	}
	client.authenticated = true
	return nil
}

// Search authenticates if necessary, executes SPL, and buffers the unmodified
// response in Result.Data. Prefer SearchTo when the response may be large. In
// job mode, cancellation after dispatch causes a best-effort remote
// cancellation. Export mode has no JobID or search.log.
func (client *Client) Search(ctx context.Context, search string, options SearchOptions) (Result, error) {
	var output bytes.Buffer
	result, err := client.SearchTo(ctx, search, options, &output)
	if err != nil {
		return result, err
	}
	result.Data = bytes.Clone(output.Bytes())
	return result, nil
}

// SearchTo authenticates if necessary and streams the unmodified Splunk
// response directly to output. It does not buffer response data in Result.Data.
// If output returns an error, SearchTo returns that error after any bytes
// already accepted by output; Result still contains available job provenance.
func (client *Client) SearchTo(ctx context.Context, search string, options SearchOptions, output io.Writer) (Result, error) {
	if strings.TrimSpace(search) == "" {
		return Result{}, ErrEmptySearch
	}
	if output == nil {
		return Result{}, fmt.Errorf("%w: output writer is required", ErrInvalidConfig)
	}
	if err := validateSearchOptions(options); err != nil {
		return Result{}, err
	}
	if err := client.Authenticate(ctx); err != nil {
		return Result{}, err
	}

	client.mu.Lock()
	conn := client.conn
	client.mu.Unlock()

	query := queryState{Query: search}
	dispatch := dispatchOptions{
		DispatchParams:     cloneParams(options.DispatchParams),
		ResultParams:       cloneParams(options.ResultParams),
		ResultEndpointMode: options.ResultEndpoint,
		ExecutionMode:      options.ExecutionMode,
		SearchLogMode:      options.SearchLog,
		SearchLogFile:      options.SearchLogFile,
	}
	if err := conn.dispatchQuery(ctx, &query, dispatch, output); err != nil {
		return resultFromQuery(query, nil), err
	}
	return resultFromQuery(query, nil), nil
}

// Close releases idle connections created by NewClient. It is safe to call
// repeatedly. Caller-supplied HTTP clients remain owned by the caller.
func (client *Client) Close() error {
	if client != nil && client.ownedClient != nil {
		client.ownedClient.CloseIdleConnections()
	}
	return nil
}

func validateSearchOptions(options SearchOptions) error {
	if options.ExecutionMode != "" && options.ExecutionMode != ExecutionModeJob && options.ExecutionMode != ExecutionModeExport {
		return fmt.Errorf("%w: unsupported execution mode %q", ErrInvalidConfig, options.ExecutionMode)
	}
	if options.ResultEndpoint != "" && options.ResultEndpoint != ResultEndpointAuto && options.ResultEndpoint != ResultEndpointV1 && options.ResultEndpoint != ResultEndpointV2 {
		return fmt.Errorf("%w: unsupported result endpoint %q", ErrInvalidConfig, options.ResultEndpoint)
	}
	if options.SearchLog != "" && options.SearchLog != SearchLogModeOff && options.SearchLog != SearchLogModeSummary && options.SearchLog != SearchLogModeSave && options.SearchLog != SearchLogModeBoth {
		return fmt.Errorf("%w: unsupported search log mode %q", ErrInvalidConfig, options.SearchLog)
	}
	if options.ExecutionMode == ExecutionModeExport && (options.SearchLog == SearchLogModeSave || options.SearchLog == SearchLogModeBoth || strings.TrimSpace(options.SearchLogFile) != "") {
		return fmt.Errorf("%w: export mode cannot save search.log", ErrInvalidConfig)
	}
	return nil
}

func cloneParams(params map[string][]string) map[string][]string {
	if params == nil {
		return nil
	}
	cloned := make(map[string][]string, len(params))
	for key, values := range params {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func resultFromQuery(query queryState, data []byte) Result {
	return Result{
		Data:          data,
		JobID:         query.Job.Sid,
		State:         query.State,
		Diagnostics:   query.LogDiagnostics,
		SearchLogRead: query.SearchLogRead,
		SearchLogFile: query.SearchLogFile,
	}
}
