package splunk

import "testing"

func FuzzAnalyzeJobLog(f *testing.F) {
	f.Add("INFO Search completed in 1.25 seconds\nWARN DispatchThread - synthetic warning")
	f.Add("ERROR SearchOperator - synthetic failure")
	f.Add("")
	f.Fuzz(func(t *testing.T, input string) {
		diagnostics := AnalyzeJobLog(input)
		if len(diagnostics.Warnings) > maxDiagnosticLines || len(diagnostics.Errors) > maxDiagnosticLines {
			t.Fatal("diagnostic bounds exceeded")
		}
	})
}
