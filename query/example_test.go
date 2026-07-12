package query_test

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/georgestarcher/querysplunk/v2/query"
	"github.com/georgestarcher/querysplunk/v2/splunk"
)

func ExampleLoad() {
	config, err := query.Load(strings.NewReader("search: search index=_internal earliest=-15m | stats count\n"))
	if err != nil {
		return
	}
	prepared, err := query.Prepare(config, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		var violation *query.ViolationError
		_ = errors.Is(err, query.ErrSafetyViolation)
		_ = errors.As(err, &violation)
		return
	}
	_ = prepared.Findings()
}

func ExamplePrepared_Plan() {
	config, err := query.Load(strings.NewReader("search: search index=_internal earliest=-15m | stats count\n"))
	if err != nil {
		return
	}
	prepared, err := query.Prepare(config, query.Overrides{}, query.SafetyPolicy{})
	if err != nil {
		return
	}
	_ = prepared.Plan() // Credential-free effective config and structured safety findings.
}

func ExamplePrepared_SearchTo() {
	var client *splunk.Client // Construct with splunk.NewClient; credentials stay outside YAML.
	config, err := query.Load(strings.NewReader("search: search index=_internal earliest=-15m | stats count\n"))
	if err != nil {
		return
	}
	prepared, err := query.Prepare(config, query.Overrides{AllowOldEarliest: true}, query.SafetyPolicy{})
	if err != nil || client == nil {
		return
	}
	_, _ = prepared.SearchTo(context.Background(), client, io.Discard)
}
