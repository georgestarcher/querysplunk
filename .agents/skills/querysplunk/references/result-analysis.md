# Result analysis playbook

Use this playbook after results or a search log have been saved. Preserve raw
artifacts unchanged and keep sensitive data out of chat.

## Bounded inspection

1. Confirm the configured output path rather than guessing a filename.
2. Check file size before reading. Do not load or print an unbounded file.
3. Identify the output format and count records first.
4. Sample only enough records to identify fields and result shape.
5. Prefer aggregate counts, distributions, ranges, and representative field
   names over raw event reproduction.
6. Report where the unchanged artifact was saved.

For JSON, use bounded tools such as `jq` to count and sample. Do not assume every
Splunk output mode has the same top-level shape; inspect keys before choosing an
expression. For CSV or XML, use format-aware bounded reads.

## Keep outputs separate

- Result files contain Splunk search output.
- JSON Lines captured from stderr contain querysplunk runtime events.
- `search.log` contains execution diagnostics and can be sensitive.

Never merge these streams. Runtime events may be summarized by kind, state,
counts, duration, and outcome. Search-log analysis should use querysplunk's
bounded diagnostics rather than dumping the complete log.

## Recommended summary

Report:

- search outcome and SID when available;
- result count and output file;
- important fields and bounded aggregate observations;
- execution duration;
- warning and error counts;
- endpoint fallback or cancellation when applicable;
- any limitation that prevents a reliable interpretation.

Do not quote credentials, private URLs, raw authorization failures, sensitive
events, complete SPL, or complete diagnostic lines.
