# AI-agent detection workflow

Use the experimental searches in
`examples/detections/ai-agent/` only as bounded threat-hunting leads.

## Preserve the command boundary

The Splunk `ai` search command enriches rows and returns fields such as
`ai_result_1`. It does not independently email, alert, write, run scripts,
dispatch searches, or select tools.

Never say that `ai` took an action merely because its prompt contains an action
verb. Report action capability only when the same audited SPL contains a
separate action-capable command. Report AI-derived dynamic execution only when
the SPL also references an `ai_result_N` field near `map` or `script`.

## Select the search

- Use `sensitive-data-enrichment.yml` when the user asks whether AI enrichment
  touched credential-bearing sources or fields. Describe matches as potential
  sensitive enrichment, not exfiltration.
- Use `downstream-action-pipeline.yml` when the user asks whether `ai` appears
  before an external, alert, write, or script command. State that co-occurrence
  does not prove the downstream command consumed AI output.
- Use `dynamic-execution-pipeline.yml` when the user asks whether AI-generated
  result fields appear to flow into `map` or `script`.

## Execute and report

1. Validate the selected YAML offline and show the bounded plan.
2. Require approval before contacting Splunk.
3. Keep the default 24-hour bound and 100-row cap unless the user explicitly
   approves a different scope.
4. Save results to an owner-only file and provide summary counts only.
5. Do not display raw audit `search`, prompts, AI output, credentials, or full
   search logs.
6. If a result needs investigation, privately inspect command order, referenced
   fields, destinations, permissions, job outcome, and artifacts.
7. Distinguish observed facts from inferences and list plausible authorized use.

Empty results mean only that no matching visible audit records were found in
the selected time range. They do not prove that AI was unused or that the
deployment is secure.

## AI enrichment before `delete`

Use `examples/detections/ai-agent/ai-assisted-delete-pipeline.yml` to find SPL
where an `ai` command occurs before a `delete` command. Treat the result as a
high-impact investigation lead. The Splunk `ai` command enriches rows and does
not perform actions itself; `delete` is the separate command that makes the
selected events unavailable to subsequent searches.

An `ai_result_N` reference is supporting context, not a match requirement.
Never test this detection by dispatching a real `delete` command. Use
synthetic audit rows for positive and negative validation.
