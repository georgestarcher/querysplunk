# Recent scheduled-search job failures

Use this workflow when the user asks, "Check this search for any recent job
failures," or makes an equivalent request about a named Splunk saved search.

1. Resolve "this search" to one exact saved-search name. Ask for the name only
   when it is not available from the conversation or supplied artifact.
2. Default the recent window to 24 hours unless the user specifies another
   bounded period.
3. Search scheduler history across every app with `app="*"`. Do not pass `*` as
   the MCP tool's app-context argument; omit that argument and keep the wildcard
   in SPL.
4. Replace `REPLACE_WITH_EXACT_SAVED_SEARCH_NAME` in the query below. Escape any
   backslash or double quote that is part of the name before placing it in the
   SPL string literal.
5. Prefer the Splunk MCP query tool when available. Pass the time bounds through
   the MCP arguments and run the SPL beginning with `search`; this query does not
   require the MCP-prohibited `rest` command.
6. Otherwise, put the bounded query below in a generated YAML config, validate
   it, and run it with querysplunk.
7. When `non_success_runs` is greater than zero and `latest_issue_sid` is
   present, fetch that job's `search.log` with querysplunk:

   ```bash
   querysplunk -job-sid <latest_issue_sid> -job-action search-log
   ```

   The Splunk MCP query tool can identify the SID but does not expose a
   `search.log` retrieval tool, and its guardrails reject the REST command that
   would fetch the log. Use querysplunk for this step. Treat a missing or expired
   job artifact as unavailable evidence rather than a new failure. Summarize
   relevant `ERROR`, `WARN`, fatal, cancellation, and performance lines; do not
   dump the complete log into chat.

```spl
search index=_internal source=*scheduler.log app="*" savedsearch_name="REPLACE_WITH_EXACT_SAVED_SEARCH_NAME"
| eval is_issue=if(isnotnull(status) AND status!="success", 1, 0)
| eval issue_time=if(is_issue=1, _time, null())
| eval issue_reason=if(is_issue=1, coalesce(reason, "No reason reported"), null())
| eval issue_sid=if(is_issue=1, sid, null())
| stats count AS total_runs
        count(eval(status="success")) AS successful_runs
        count(eval(status="failed")) AS failed_runs
        count(eval(status="skipped")) AS skipped_runs
        sum(is_issue) AS non_success_runs
        latest(_time) AS latest_run_epoch
        latest(issue_time) AS latest_issue_epoch
        latest(status) AS latest_status
        latest(issue_reason) AS latest_issue_reason
        latest(issue_sid) AS latest_issue_sid
        values(app) AS observed_apps
        values(status) AS observed_statuses
| eval latest_run_time=if(total_runs>0, strftime(latest_run_epoch, "%Y-%m-%d %H:%M:%S %Z"), "none")
| eval latest_issue_time=if(non_success_runs>0, strftime(latest_issue_epoch, "%Y-%m-%d %H:%M:%S %Z"), "none")
| table total_runs successful_runs failed_runs skipped_runs non_success_runs latest_run_time latest_issue_time latest_status latest_issue_reason latest_issue_sid observed_apps observed_statuses
```

Interpret the result conservatively:

- `total_runs=0`: no scheduler evidence was visible in the selected window. Do
  not report the search as healthy; note that it may not have run, may be
  disabled, may have a different name, or may be outside the caller's access.
- `total_runs>0` and `non_success_runs=0`: no recent failed, skipped, or other
  non-successful runs were found.
- `non_success_runs>0`: report failed and skipped counts, the latest issue time,
  reason, and SID. Call out an observed non-success status even when it is not
  literally `failed` or `skipped`.

This checks scheduler outcomes. If the user needs errors or warnings emitted by
a completed job whose scheduler status is `success`, retrieve its SID with the
same pattern and inspect that job's `search.log` separately.

## Analyze retained failed-job logs across searches

When the user asks to find failed searches whose logs are still available,
follow `scheduled-search-log-analysis.md` and start from
`../templates/recent-search-job-failures.yml`. That workflow intentionally
differs from the exact-name check above: it intersects bounded scheduler history
with Splunk's retained-job inventory, analyzes up to 10 recent unique failed
searches, and requires a configured Splunk `ai` command.
