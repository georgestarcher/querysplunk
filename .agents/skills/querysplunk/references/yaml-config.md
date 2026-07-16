# YAML config reference for assistants

Use `-write-config` to generate the current skeleton instead of inventing YAML fields:

```bash
querysplunk -write-config search.yml
```

Generated and bundled search files use schema version `1`:

```yaml
schema_version: "1"
```

Existing runtime-only files without `schema_version` remain compatible and are interpreted as version 1. New files should declare the version explicitly.

Validate a generated or edited config before live execution:

```bash
querysplunk -validate-config search.yml
```

Validation is offline and prints the effective config plus structured safety
findings. It does not load credentials or prove that Splunk will authorize or
execute the search. An exported `SPLUNKAPP` is used only when YAML omits `app`;
an explicit `-app` override takes precedence.

Important fields:

- `schema_version`: public YAML contract version. The current value is `"1"`.
- `metadata`: optional stable identity, title, description, category, lifecycle status, revision, author, severity, and tags.
- `requirements`: optional supported platforms and required apps, data models, indexes, fields, or capabilities.
- `provenance`: optional source project, URL, rule IDs, revision, license, and adaptation notes.
- `interpretation`: optional result summary, false positives, and recommended actions.
- `app`: Splunk app namespace.
- `output_file`: where querysplunk writes the raw Splunk response body.
- `mode`: `job` or `export`.
- `search`: SPL text.
- `safety`: explicit acknowledgements for high-impact searches, including `allow_old_earliest` and `allow_index_wildcard`.
- `dispatch`: dispatch parameters such as `earliest_time`, `latest_time`, `max_count`, `status_buckets`, and `required_fields`.
- `results`: result endpoint, output mode, count, and offset.
- `diagnostics.search_log`: `off`, `summary`, `save`, or `both`.

When a descriptive block is present, querysplunk validates its required fields. Bundled out-of-box searches include all four descriptive blocks; compact user-created files may omit them. Metadata IDs must be lowercase namespaced values such as `querysplunk.health.scheduler-health`. Lifecycle status is one of `experimental`, `stable`, `deprecated`, or `retired`; severity is one of `informational`, `low`, `medium`, `high`, or `critical`.

Secrets never belong in YAML. Use environment variables or a local `.env` mechanism for credentials.

The public Go package `github.com/georgestarcher/querysplunk/v2/query` owns this
schema. Its loading APIs strictly reject unknown fields, duplicate keys,
malformed or multiple YAML documents, missing search text, invalid modes, and
incompatible diagnostics. Use `query.Prepare` after `query.Overrides`; its
zero-value safety policy matches the CLI. Typed findings distinguish warnings,
blocking violations, and acknowledgements. `query.UnsafeAllowAll()` deliberately
disables blocking protections and is unsafe for untrusted searches.

For `mode: job`, querysplunk creates a Splunk search job, polls for completion, can fetch `search.log`, and then fetches results.

For `mode: export`, querysplunk streams results directly from Splunk export. This can be useful for large exports, but there is no search job ID and no `search.log` diagnostic fetch.

## Schema design acknowledgment

The descriptive model was inspired by the metadata and provenance discipline used by [Agent Threat Rules (ATR)](https://github.com/Agent-Threat-Rule/agent-threat-rules), reviewed at pinned revision [`0c7a1f133fc176a732767363db65102aa0aae710`](https://github.com/Agent-Threat-Rule/agent-threat-rules/tree/0c7a1f133fc176a732767363db65102aa0aae710). See the [ATR schema](https://github.com/Agent-Threat-Rule/agent-threat-rules/blob/0c7a1f133fc176a732767363db65102aa0aae710/spec/atr-schema.yaml) and [MIT license](https://github.com/Agent-Threat-Rule/agent-threat-rules/blob/0c7a1f133fc176a732767363db65102aa0aae710/LICENSE).

querysplunk's schema and SPL are original to this project; this change includes no ATR rule text or code. Any future substantial ATR adaptation must identify its source rule and revision and preserve the applicable MIT notice.
