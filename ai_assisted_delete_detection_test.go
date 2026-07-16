package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/georgestarcher/querysplunk/v2/query"
)

func TestAIAssistedDeleteDetectionFixtures(t *testing.T) {
	t.Parallel()

	config, err := query.LoadFile("examples/detections/ai-agent/ai-assisted-delete-pipeline.yml")
	if err != nil {
		t.Fatalf("load AI-assisted delete detection: %v", err)
	}

	commandOrder := regexp.MustCompile(`(?is)\|\s*ai(?:\s|$).*?\|\s*delete\b`)
	quotedString := regexp.MustCompile(`(?s)"(?:\\.|[^"\\])*"`)
	structuralSearch := func(search string) string {
		return quotedString.ReplaceAllString(search, `""`)
	}

	positive := []string{
		`search index=example | ai prompt="classify each result" | delete`,
		"search index=example\n| AI prompt=\"decide\"\n| eval selected=ai_result_1=\"yes\"\n| where selected\n| DELETE",
	}
	for _, search := range positive {
		if !commandOrder.MatchString(structuralSearch(search)) {
			t.Errorf("expected positive fixture to match: %q", search)
		}
	}

	negative := []string{
		`search index=example | delete | ai prompt="explain the result"`,
		`search index=example | ai prompt="explain why | delete is risky"`,
		`search index=example | ai prompt="classify each result" | table ai_result_1`,
		config.Search,
	}
	for _, search := range negative {
		if commandOrder.MatchString(structuralSearch(search)) {
			t.Errorf("expected negative fixture not to match: %q", search)
		}
	}

	for _, fragment := range []string{`"a"."i"`, `"d"."e"."l"."e"."t"."e"`} {
		if !strings.Contains(config.Search, fragment) {
			t.Errorf("detection SPL must construct %s from fragments", fragment)
		}
	}
}
