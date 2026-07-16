# AI-agent detection starter pack

These experimental searches use Splunk audit data to identify risky ways the
Splunk `ai` search command is combined with data sources and later commands.
They are threat-hunting leads, not automatic blocking or complete AI security
coverage.

## Important command boundary

The Splunk `ai` command enriches or extracts features from rows already in the
search pipeline. It writes generated values to result fields such as
`ai_result_1`. It does not independently email data, write results, run a
script, dispatch a dynamic search, or select an external tool.

An action claim therefore requires a separate action-capable SPL command in the
same audited search. Even then, command co-occurrence does not prove that AI
output influenced the action. The dynamic-execution detection is narrower
because it also requires an `ai_result_N` field near `map` or `script`.

## Included searches

- `sensitive-data-enrichment.yml` finds `ai` use in searches that reference the
  stored-credential REST endpoint, credential-named lookups, or clear/encrypted
  password fields. It indicates potential sensitive enrichment, not
  exfiltration.
- `downstream-action-pipeline.yml` finds `ai` followed by `sendemail`,
  `sendalert`, `collect`, `outputlookup`, or `script`. It indicates paired
  action capability, but the downstream command can be independent of AI
  output.
- `dynamic-execution-pipeline.yml` finds `map` or `script` referencing an
  `ai_result_N` field after `ai`. It is a higher-risk data-flow lead, but still
  requires private inspection of the full SPL and resulting jobs.

All three searches use `_audit`, default to the last 24 hours, cap output at
100 rows, and omit raw SPL, prompts, AI output, and credentials. They return a
SHA-256 correlation value and input length instead. A hash is pseudonymous and
remains sensitive.

## Run safely

Validate without contacting Splunk:

```bash
querysplunk -validate-config examples/detections/ai-agent/sensitive-data-enrichment.yml
querysplunk -validate-config examples/detections/ai-agent/downstream-action-pipeline.yml
querysplunk -validate-config examples/detections/ai-agent/dynamic-execution-pipeline.yml
```

After reviewing the effective plan, run one search:

```bash
querysplunk -config examples/detections/ai-agent/sensitive-data-enrichment.yml
```

The Splunk role needs permission to search `_audit`. Keep result files private,
display summaries only in an AI assistant, and inspect raw audited SPL only in
an authorized private workflow.

## Evidence limits

The pack does not observe the row values sent to `ai`, durable AI output,
model selection, token usage, or exact model latency. It also cannot prove that
a downstream command consumed AI output unless the audited SPL explicitly
references an `ai_result_N` field.

ATR-2026-00511 is intentionally omitted because no durable MCP web-fetch or
tool-output content stream was observed. ATR-2026-00030 is intentionally
omitted because no reliable inter-agent message stream or cross-source session
join was observed. See the [telemetry mapping](../AI-COMMAND-TELEMETRY.md) for
the tested field contract.

## Attribution and tests

The pack was inspired by stable Agent Threat Rules at pinned revision
`0c7a1f133fc176a732767363db65102aa0aae710`. Each YAML names its source rule,
revision, license, and adaptation boundary. The SPL and Splunk field mapping are
original to querysplunk rather than converter output.

Deterministic Go tests execute synthetic positive and negative SPL strings
against the exact detection patterns embedded in the YAML. No live prompts,
events, credentials, URLs, or tenant details are used. See
[THIRD_PARTY_NOTICES.md](../../../THIRD_PARTY_NOTICES.md) for the ATR notice and
MIT license.

### AI enrichment preceding event hiding

`ai-assisted-delete-pipeline.yml` reports searches where an `ai` command
precedes a `delete` command in the same SPL pipeline. Intermediate `eval`,
`where`, or other commands are optional. An `ai_result_N` reference adds
context but is not required. The finding means that AI enrichment preceded
Splunk event hiding; it does not mean that the `ai` command performed the
deletion or acted autonomously.

The detector constructs the command names from fragments and strips quoted
strings before matching. This prevents the detector from matching its own SPL
or a prompt that merely discusses the `delete` command.
