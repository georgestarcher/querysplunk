package splunk_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func ExampleClient() {
	client, err := splunk.NewClient(splunk.Config{
		BaseURL: os.Getenv("SPLUNKBASEURL"),
		Token:   os.Getenv("SPLUNKTOKEN"),
		Timeout: 2 * time.Minute,
		App:     "search",
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if errors.Is(err, splunk.ErrInvalidConfig) || err != nil {
		return
	}
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := client.Authenticate(ctx); err != nil {
		return
	}

	result, err := client.Search(ctx,
		"search index=_internal earliest=-15m | stats count",
		splunk.SearchOptions{
			DispatchParams: map[string][]string{"latest_time": {"now"}},
			ResultParams:   map[string][]string{"output_mode": {"json"}},
			SearchLog:      splunk.SearchLogModeSummary,
		},
	)
	if err != nil {
		var statusErr *splunk.HTTPStatusError
		var stateErr *splunk.JobStateError
		_ = errors.Is(err, context.DeadlineExceeded)
		_ = errors.As(err, &statusErr)
		_ = errors.As(err, &stateErr)
		return
	}
	_ = result.Data // decode according to the requested Splunk output mode
}

func ExampleClient_InspectJob() {
	client, err := splunk.NewClient(splunk.Config{
		BaseURL: os.Getenv("SPLUNKBASEURL"),
		Token:   os.Getenv("SPLUNKTOKEN"),
		App:     "search",
	})
	if err != nil {
		return
	}
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := client.InspectJob(ctx, "1258421375.19")
	if err != nil {
		return
	}
	if !status.Terminal {
		status, err = client.WaitJob(ctx, status.JobID)
	}
	_ = status
	_ = err
}
