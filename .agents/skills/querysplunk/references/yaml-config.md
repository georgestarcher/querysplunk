# YAML config reference for assistants

Use `-write-config` to generate the current skeleton instead of inventing YAML fields:

```bash
querysplunk -write-config search.yml
```

Validate a generated or edited config before live execution:

```bash
querysplunk -validate-config search.yml
```

Validation is offline and prints the effective config plus structured safety
findings. It does not load credentials or prove that Splunk will authorize or
execute the search. An exported `SPLUNKAPP` is used only when YAML omits `app`;
an explicit `-app` override takes precedence.

Important fields:

- `app`: Splunk app namespace.
- `output_file`: where querysplunk writes the raw Splunk response body.
- `mode`: `job` or `export`.
- `search`: SPL text.
- `safety`: explicit acknowledgements for high-impact searches, including `allow_old_earliest` and `allow_index_wildcard`.
- `dispatch`: dispatch parameters such as `earliest_time`, `latest_time`, `max_count`, `status_buckets`, and `required_fields`.
- `results`: result endpoint, output mode, count, and offset.
- `diagnostics.search_log`: `off`, `summary`, `save`, or `both`.

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
