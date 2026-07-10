# YAML config reference for assistants

Use `-write-config` to generate the current skeleton instead of inventing YAML fields:

```bash
querysplunk -write-config search.yml
```

Important fields:

- `app`: Splunk app namespace.
- `output_file`: where querysplunk writes the raw Splunk response body.
- `mode`: `job` or `export`.
- `search`: SPL text.
- `dispatch`: dispatch parameters such as `earliest_time`, `latest_time`, `max_count`, `status_buckets`, and `required_fields`.
- `results`: result endpoint, output mode, count, and offset.
- `diagnostics.search_log`: `off`, `summary`, `save`, or `both`.

Secrets never belong in YAML. Use environment variables or a local `.env` mechanism for credentials.

For `mode: job`, querysplunk creates a Splunk search job, polls for completion, can fetch `search.log`, and then fetches results.

For `mode: export`, querysplunk streams results directly from Splunk export. This can be useful for large exports, but there is no search job ID and no `search.log` diagnostic fetch.
