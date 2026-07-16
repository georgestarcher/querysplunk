package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/georgestarcher/querysplunk/v2/splunk"
)

// ResultClassification describes the sensitivity of a search result.
type ResultClassification string

const (
	ResultClassificationNormal    ResultClassification = "normal"
	ResultClassificationInternal  ResultClassification = "internal"
	ResultClassificationSensitive ResultClassification = "sensitive"
	ResultClassificationSecret    ResultClassification = "secret"
)

// AgentDisplayMode controls how an AI assistant should present result data.
type AgentDisplayMode string

const (
	AgentDisplayNormal         AgentDisplayMode = "normal"
	AgentDisplayBoundedSummary AgentDisplayMode = "bounded_summary"
	AgentDisplaySummaryOnly    AgentDisplayMode = "summary_only"
	AgentDisplayDoNotDisplay   AgentDisplayMode = "do_not_display"
)

// ResultRetention describes the recommended result-file lifetime.
type ResultRetention string

const (
	ResultRetentionNormal    ResultRetention = "normal"
	ResultRetentionTemporary ResultRetention = "temporary"
)

// ResultHandling declares how callers should protect and present search output.
// It never changes Splunk authorization or makes unsafe SPL safe.
type ResultHandling struct {
	Classification      ResultClassification `json:"classification" yaml:"classification"`
	ContainsCredentials bool                 `json:"contains_credentials" yaml:"contains_credentials"`
	AgentDisplay        AgentDisplayMode     `json:"agent_display" yaml:"agent_display"`
	RecommendedFileMode string               `json:"recommended_file_mode" yaml:"recommended_file_mode"`
	Retention           ResultRetention      `json:"retention" yaml:"retention"`
}

// ResultContract describes structural checks applied to JSON results after
// retrieval and before SearchToFile publishes its atomic output.
type ResultContract struct {
	RequiredFields []string `json:"required_fields" yaml:"required_fields"`
	AllowEmpty     bool     `json:"allow_empty" yaml:"allow_empty"`
	MaximumRows    int      `json:"maximum_rows" yaml:"maximum_rows"`
}

// ResultContractSummary reports bounded structural validation information.
type ResultContractSummary struct {
	Enforced bool `json:"enforced" yaml:"enforced"`
	Rows     int  `json:"rows" yaml:"rows"`
}

// ResultContractErrorKind identifies a result-contract failure without
// including result values.
type ResultContractErrorKind string

const (
	ResultContractInvalidJSON  ResultContractErrorKind = "invalid_json"
	ResultContractInvalidShape ResultContractErrorKind = "invalid_shape"
	ResultContractEmpty        ResultContractErrorKind = "empty_results"
	ResultContractMissingField ResultContractErrorKind = "missing_field"
	ResultContractRowLimit     ResultContractErrorKind = "row_limit"
)

var (
	// ErrResultContract identifies a post-retrieval structural contract failure.
	ErrResultContract = errors.New("result contract violation")

	// FindingResultContainsCredentials identifies a non-secret warning emitted
	// before executing a search declared to return credential material.
	FindingResultContainsCredentials = "result_contains_credentials"
)

// ResultContractError reports only structural context. It never includes a
// row value or raw response fragment.
type ResultContractError struct {
	Kind        ResultContractErrorKind
	Row         int
	Field       string
	MaximumRows int
	ActualRows  int
	Err         error
}

func (contractError *ResultContractError) Error() string {
	if contractError == nil {
		return ErrResultContract.Error()
	}
	switch contractError.Kind {
	case ResultContractInvalidJSON:
		return "result contract violation: response is not valid JSON"
	case ResultContractInvalidShape:
		return "result contract violation: response does not use a supported Splunk JSON result shape"
	case ResultContractEmpty:
		return "result contract violation: response contains no result rows"
	case ResultContractMissingField:
		return fmt.Sprintf("result contract violation: row %d is missing required field %q", contractError.Row, contractError.Field)
	case ResultContractRowLimit:
		return fmt.Sprintf("result contract violation: response contains more than %d rows", contractError.MaximumRows)
	default:
		return ErrResultContract.Error()
	}
}

func (contractError *ResultContractError) Is(target error) bool {
	return target == ErrResultContract
}

func (contractError *ResultContractError) Unwrap() error {
	if contractError == nil {
		return nil
	}
	return contractError.Err
}

func validateResultHandling(handling *ResultHandling) error {
	if handling == nil {
		return nil
	}
	switch handling.Classification {
	case ResultClassificationNormal, ResultClassificationInternal, ResultClassificationSensitive, ResultClassificationSecret:
	default:
		return fmt.Errorf("%w: result_handling.classification %q must be normal, internal, sensitive, or secret", ErrInvalidConfig, handling.Classification)
	}
	switch handling.AgentDisplay {
	case AgentDisplayNormal, AgentDisplayBoundedSummary, AgentDisplaySummaryOnly, AgentDisplayDoNotDisplay:
	default:
		return fmt.Errorf("%w: result_handling.agent_display %q must be normal, bounded_summary, summary_only, or do_not_display", ErrInvalidConfig, handling.AgentDisplay)
	}
	if handling.RecommendedFileMode != "0600" {
		return fmt.Errorf("%w: result_handling.recommended_file_mode must be \"0600\"", ErrInvalidConfig)
	}
	switch handling.Retention {
	case ResultRetentionNormal, ResultRetentionTemporary:
	default:
		return fmt.Errorf("%w: result_handling.retention %q must be normal or temporary", ErrInvalidConfig, handling.Retention)
	}
	if handling.Classification == ResultClassificationSecret && handling.AgentDisplay != AgentDisplayDoNotDisplay {
		return fmt.Errorf("%w: secret results require result_handling.agent_display do_not_display", ErrInvalidConfig)
	}
	if handling.ContainsCredentials {
		if handling.Classification != ResultClassificationSecret {
			return fmt.Errorf("%w: credential-bearing results require result_handling.classification secret", ErrInvalidConfig)
		}
		if handling.AgentDisplay != AgentDisplayDoNotDisplay {
			return fmt.Errorf("%w: credential-bearing results require result_handling.agent_display do_not_display", ErrInvalidConfig)
		}
		if handling.Retention != ResultRetentionTemporary {
			return fmt.Errorf("%w: credential-bearing results require result_handling.retention temporary", ErrInvalidConfig)
		}
	}
	return nil
}

func validateResultContract(contract *ResultContract, outputMode string) error {
	if contract == nil {
		return nil
	}
	if err := validateStringList("result_contract.required_fields", contract.RequiredFields); err != nil {
		return err
	}
	if contract.MaximumRows < 0 {
		return fmt.Errorf("%w: result_contract.maximum_rows cannot be negative", ErrInvalidConfig)
	}
	if mode := strings.TrimSpace(outputMode); mode != "" && mode != "json" {
		return fmt.Errorf("%w: result_contract requires results.output_mode json", ErrInvalidConfig)
	}
	return nil
}

// ValidateResult applies the prepared JSON result contract to a Splunk result
// stream. When no contract is configured, it does not consume reader.
func (prepared Prepared) ValidateResult(reader io.Reader) (ResultContractSummary, error) {
	if prepared.config.ResultContract == nil {
		return ResultContractSummary{}, nil
	}
	if reader == nil {
		return ResultContractSummary{Enforced: true}, &ResultContractError{Kind: ResultContractInvalidJSON}
	}
	rows, err := validateJSONResultContract(reader, *prepared.config.ResultContract)
	return ResultContractSummary{Enforced: true, Rows: rows}, err
}

func (prepared Prepared) validateResultBytes(data []byte) error {
	if prepared.config.ResultContract == nil {
		return nil
	}
	_, err := prepared.ValidateResult(bytes.NewReader(data))
	return err
}

func (prepared Prepared) searchToWithResultContract(ctx context.Context, client *splunk.Client, output io.Writer) (splunk.Result, error) {
	if prepared.config.ResultContract == nil {
		return client.SearchTo(ctx, prepared.config.Search, cloneOptions(prepared.options), output)
	}
	if output == nil {
		return splunk.Result{}, fmt.Errorf("%w: output writer is required", ErrInvalidConfig)
	}
	spool, err := os.CreateTemp("", ".querysplunk-contract-*")
	if err != nil {
		return splunk.Result{}, err
	}
	spoolPath := spool.Name()
	defer func() {
		_ = spool.Close()
		if spoolPath != "" {
			_ = os.Remove(spoolPath)
		}
	}()
	if err := spool.Chmod(0600); err != nil {
		return splunk.Result{}, err
	}
	if err := os.Remove(spoolPath); err == nil {
		spoolPath = ""
	}

	result, err := client.SearchTo(ctx, prepared.config.Search, cloneOptions(prepared.options), spool)
	if err != nil {
		return result, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	if _, err := prepared.ValidateResult(spool); err != nil {
		return result, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	if _, err := io.Copy(output, spool); err != nil {
		return result, err
	}
	return result, nil
}

func validateJSONResultContract(reader io.Reader, contract ResultContract) (int, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	rows := 0
	documents := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rows, &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
		}
		documents++
		delimiter, ok := token.(json.Delim)
		if !ok {
			return rows, &ResultContractError{Kind: ResultContractInvalidShape}
		}
		switch delimiter {
		case '{':
			if err := consumeResultEnvelope(decoder, contract, &rows); err != nil {
				return rows, err
			}
		case '[':
			if err := consumeResultRows(decoder, contract, &rows); err != nil {
				return rows, err
			}
		default:
			return rows, &ResultContractError{Kind: ResultContractInvalidShape}
		}
	}
	if documents == 0 {
		if contract.AllowEmpty {
			return 0, nil
		}
		return 0, &ResultContractError{Kind: ResultContractEmpty}
	}
	if rows == 0 && !contract.AllowEmpty {
		return 0, &ResultContractError{Kind: ResultContractEmpty}
	}
	return rows, nil
}

func consumeResultEnvelope(decoder *json.Decoder, contract ResultContract, rows *int) error {
	recognized := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
		}
		key, ok := keyToken.(string)
		if !ok {
			return &ResultContractError{Kind: ResultContractInvalidShape}
		}
		switch key {
		case "results":
			recognized = true
			start, err := decoder.Token()
			if err != nil {
				return &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
			}
			if start != json.Delim('[') {
				return &ResultContractError{Kind: ResultContractInvalidShape}
			}
			if err := consumeResultRows(decoder, contract, rows); err != nil {
				return err
			}
		case "result":
			recognized = true
			if err := consumeResultRow(decoder, contract, rows); err != nil {
				return err
			}
		default:
			if key == "preview" || key == "lastrow" || key == "messages" || key == "fields" || key == "init_offset" || key == "offset" {
				recognized = true
			}
			var discarded json.RawMessage
			if err := decoder.Decode(&discarded); err != nil {
				return &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
	}
	if !recognized {
		return &ResultContractError{Kind: ResultContractInvalidShape}
	}
	return nil
}

func consumeResultRows(decoder *json.Decoder, contract ResultContract, rows *int) error {
	for decoder.More() {
		if err := consumeResultRow(decoder, contract, rows); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
	}
	return nil
}

func consumeResultRow(decoder *json.Decoder, contract ResultContract, rows *int) error {
	var row map[string]json.RawMessage
	if err := decoder.Decode(&row); err != nil {
		return &ResultContractError{Kind: ResultContractInvalidJSON, Err: err}
	}
	if row == nil {
		return &ResultContractError{Kind: ResultContractInvalidShape}
	}
	*rows = *rows + 1
	if contract.MaximumRows > 0 && *rows > contract.MaximumRows {
		return &ResultContractError{Kind: ResultContractRowLimit, MaximumRows: contract.MaximumRows, ActualRows: *rows}
	}
	for _, field := range contract.RequiredFields {
		if _, ok := row[field]; !ok {
			return &ResultContractError{Kind: ResultContractMissingField, Row: *rows, Field: field}
		}
	}
	return nil
}
