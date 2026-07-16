package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/georgestarcher/querysplunk/v2/query"
)

const (
	aiCommandPattern        = `(?i)\|\s*ai(?:\s|$)`
	sensitiveInputPattern   = `(?is)(?:\|\s*rest\s+/services(?:NS/[^/\s]+/[^/\s]+)?/storage/passwords\b|\|\s*inputlookup\b[^\n|]{0,300}(?:credential|secret|token|password|key)[^\n|]*|\b(?:clear_password|encr_password)\b).*?\|\s*ai(?:\s|$)`
	actionPipelinePattern   = `(?is)\|\s*ai(?:\s|$).*?\|\s*(?:sendemail|sendalert|collect|outputlookup|script)\b`
	dynamicExecutionPattern = `(?is)\|\s*ai(?:\s|$).*?\|\s*(?:map|script)\b[^|]*(?:\$?ai_result_[0-9]+\$?)`
)

type aiDetectionSpec struct {
	path          string
	sourceRuleIDs []string
	patterns      []string
	requiresAI    bool
	stripQuoted   bool
	fragmented    bool
	positive      []string
	negative      []string
}

func TestAIAgentDetectionPatterns(t *testing.T) {
	t.Parallel()

	specs := []aiDetectionSpec{
		{
			path:          "examples/detections/ai-agent/sensitive-data-enrichment.yml",
			sourceRuleIDs: []string{"ATR-2026-00702"},
			patterns:      []string{sensitiveInputPattern},
			fragmented:    true,
			positive: []string{
				`| rest /services/storage/passwords | table clear_password | ai prompt="classify {clear_password}"`,
				`| inputlookup credential_inventory | ai prompt="categorize {owner}"`,
			},
			negative: []string{
				`index=main | ai prompt="summarize {message}"`,
				`| rest /services/storage/passwords | table username`,
				`| makeresults | eval note="password policy" | ai prompt="summarize {note}"`,
				`index=main | ai prompt="summarize {message}" | eval clear_password="x"`,
			},
		},
		{
			path:          "examples/detections/ai-agent/downstream-action-pipeline.yml",
			sourceRuleIDs: []string{"ATR-2026-00702"},
			patterns:      []string{actionPipelinePattern},
			stripQuoted:   true,
			positive: []string{
				`search index=main | ai prompt="classify {message}" | sendemail to="soc@example.invalid"`,
				`search index=main | ai prompt="score {message}" | outputlookup review_queue`,
			},
			negative: []string{
				`search index=main | ai prompt="classify {message}" | table ai_result_1`,
				`search index=main | sendemail to="soc@example.invalid" | ai prompt="summarize {message}"`,
				`search index=main | ai prompt="explain the sendemail command"`,
				`search index=main | ai prompt="explain why | sendemail is risky" | table ai_result_1`,
			},
		},
		{
			path:          "examples/detections/ai-agent/dynamic-execution-pipeline.yml",
			sourceRuleIDs: []string{"ATR-2026-00711", "ATR-2026-00714"},
			patterns:      []string{dynamicExecutionPattern},
			positive: []string{
				`search index=main | ai prompt="classify {message}" | map search="search index=review value=\"$ai_result_1$\""`,
				`search index=main | ai prompt="extract {message}" | script review.py ai_result_1`,
			},
			negative: []string{
				`search index=main | ai prompt="classify {message}" | map search="search index=review"`,
				`search index=main | map search="search index=review value=\"$ai_result_1$\"" | ai prompt="classify {message}"`,
				`search index=main | ai prompt="classify {message}" | table ai_result_1`,
			},
		},
	}

	for _, spec := range specs {
		spec := spec
		t.Run(spec.path, func(t *testing.T) {
			t.Parallel()

			config, err := query.LoadFile(spec.path)
			if err != nil {
				t.Fatalf("load detection YAML: %v", err)
			}
			if config.Metadata == nil || config.Metadata.Status != "experimental" {
				t.Fatal("AI-agent adaptation must remain experimental")
			}
			if config.Provenance == nil || config.Provenance.Source != "Agent Threat Rules" {
				t.Fatal("AI-agent adaptation is missing ATR provenance")
			}
			if !slices.Equal(config.Provenance.SourceRuleIDs, spec.sourceRuleIDs) {
				t.Fatalf("source rule IDs = %v, want %v", config.Provenance.SourceRuleIDs, spec.sourceRuleIDs)
			}

			compiled := make([]*regexp.Regexp, 0, len(spec.patterns))
			for _, pattern := range spec.patterns {
				if !spec.fragmented && !strings.Contains(config.Search, strconv.Quote(pattern)) {
					t.Fatalf("search does not contain tested pattern %q", pattern)
				}
				if spec.fragmented && !strings.Contains(config.Search, `"a"."i"`) {
					t.Fatal("fragmented pattern must construct the ai command name at runtime")
				}
				compiled = append(compiled, regexp.MustCompile(pattern))
			}

			for _, input := range spec.positive {
				if !matchesAIDetection(input, spec.requiresAI, spec.stripQuoted, compiled) {
					t.Errorf("positive input did not match: %q", input)
				}
			}
			for _, input := range spec.negative {
				if matchesAIDetection(input, spec.requiresAI, spec.stripQuoted, compiled) {
					t.Errorf("negative input matched: %q", input)
				}
			}

			assertNoRawAIFieldsInTable(t, config.Search)
		})
	}
}

func matchesAIDetection(input string, requiresAI, stripQuoted bool, patterns []*regexp.Regexp) bool {
	if stripQuoted {
		input = regexp.MustCompile(`(?s)"(?:\\.|[^"\\])*"`).ReplaceAllString(input, `""`)
	}
	if requiresAI && !regexp.MustCompile(aiCommandPattern).MatchString(input) {
		return false
	}
	for _, pattern := range patterns {
		if pattern.MatchString(input) {
			return true
		}
	}
	return false
}

func assertNoRawAIFieldsInTable(t *testing.T, search string) {
	t.Helper()

	for _, line := range strings.Split(search, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| table ") {
			continue
		}
		for _, field := range strings.Split(strings.TrimPrefix(line, "| table "), ",") {
			switch strings.TrimSpace(field) {
			case "search", "tool_input", "agent_output", "tool_response":
				t.Fatalf("table exposes raw sensitive field %q", strings.TrimSpace(field))
			}
		}
	}
}
