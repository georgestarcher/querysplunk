# Splunk health check YAML examples

These examples are starter YAML configs for running common read-only Splunk health checks with `querysplunk`.

They are examples, not defaults. Review each search before using it in production, especially in Splunk Cloud where some REST endpoints and internal indexes may be restricted by role or deployment type.

## Usage

Run an example with:

```bash
./splunkquery-darwin -config examples/health/splunkd-health.yml
```

Secrets do not belong in these files. Provide `SPLUNKBASEURL`, `SPLUNKTOKEN`, `SPLUNKUSERNAME`, and `SPLUNKPASSWORD` through your environment, `.env`, or GitHub Actions environment secrets.

## Examples

| File | Purpose | Common access requirements |
| --- | --- | --- |
| `splunkd-health.yml` | Quick splunkd health summary. | REST access to server health endpoints. |
| `splunkd-health-details.yml` | Feature-level splunkd health details. | REST access to server health endpoints. |
| `internal-errors-warnings.yml` | Recent `_internal` warnings and errors. | Search access to `_internal`. |
| `search-concurrency.yml` | Current search concurrency pressure. | REST access to server status endpoints. |
| `disk-partitions.yml` | Disk and partition status. | REST access to server status endpoints. |
| `resource-usage.yml` | CPU and memory resource status. | REST access to server status endpoints. |
| `scheduler-health.yml` | Scheduled search status counts over the last day. | Search access to `_internal` scheduler events. |
| `license-warnings.yml` | License-related warnings and messages. | Search access to `_internal`; availability varies in Splunk Cloud. |

## Operational notes

- `_internal` examples use bounded time windows to avoid accidental all-time searches.
- REST examples use the Splunk `rest` SPL command. Some roles cannot access these endpoints.
- Splunk Cloud tenants may hide or restrict some server-status and license endpoints.
- The examples use `mode: job` so normal job polling and optional `search.log` diagnostics remain available.
- `results.count: 0` requests all returned rows for the search result set. Add a smaller count when you want a page of results.
