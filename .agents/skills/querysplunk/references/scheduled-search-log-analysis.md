# Scheduled-search log analysis

Use these workflows when a user asks an assistant to profile successful
scheduled searches that may overrun their cadence or diagnose recent failed
jobs whose `search.log` artifacts are still retained.

## Prerequisites and boundaries

- These templates require access to `_internal`, read access to
  `/services/search/jobs` and its `search.log` child endpoint, and a configured
  Splunk `ai` command with a default model.
- Run them in the `Splunk_ML_Toolkit` app context unless the deployment exposes
  `ai` from another app.
- The templates are read-only but can be moderately expensive. They inspect at
  most 500 retained jobs, analyze at most 10 failures or five performance
  candidates, and send at most 30,000 characters of selected log text per job
  to the configured AI provider.
- Review the selected app names, search names, SIDs, and AI results as
  potentially sensitive. Do not paste raw logs into chat.
- A scheduler record can outlive its job artifact. A missing retained SID means
  the log is unavailable; it does not change the recorded job outcome.
- Do not redispatch a saved search merely to recreate an expired log.

## Diagnose retained failed jobs

Start from `../templates/recent-search-job-failures.yml` when the user asks:

- "Find failed scheduled searches whose logs are still available."
- "Explain recent saved-search failures."
- "Use the job logs to identify why scheduled searches failed."

The template examines a 24-hour scheduler window, keeps the latest failed job
for each app and saved-search name, intersects those failures with the retained
job inventory, and analyzes up to 10 logs. Add an exact `app` or
`savedsearch_name` predicate to the first scheduler search and its metadata
subsearch when the user names a narrower target.

An empty result means no failed scheduler record with an accessible retained
log passed the filters. It does not prove there were no failures. Run the
lighter exact-name workflow in `scheduler-job-failures.md` when scheduler-only
history is still useful.

## Profile long-running successful jobs

Start from `../templates/long-running-successful-searches.yml` when the user
asks:

- "Profile successful searches that are close to their schedule interval."
- "Find searches that may still be running when their next execution starts."
- "Explain why successful scheduled searches are slow."

The template calculates the gap between adjacent observed scheduler executions
for each `(app, savedsearch_name)` pair. It flags successful jobs whose runtime
is at least 80 percent of that gap, intersects them with retained jobs, keeps the
highest-risk run for up to five unique searches, and asks `ai` to analyze each
log.

Call the comparison an **observed schedule gap**, not a parsed cron interval.
Cron expressions can produce non-uniform gaps, scheduler history can be
incomplete, and schedule changes can cross the selected window. A runtime at or
above the gap is strong overlap-risk evidence, but it does not prove that
overlap caused a skipped run. Correlate with skipped scheduler statuses,
concurrency messages, workload rules, and schedule windows before assigning a
cause.

## Run safely

1. Copy the relevant template outside the installed skill directory.
2. Narrow the first scheduler search and its metadata subsearch when the user
   provides an app or saved-search name.
3. Keep the dispatch window bounded. Increase the candidate cap only with the
   user's approval because each result can invoke the AI provider.
4. Validate offline with `querysplunk -validate-config <file>`.
5. Review the effective app, time bounds, REST endpoints, map cap, and output
   path.
6. Run with `querysplunk -config <file>`.
7. Summarize the concise result table. Treat AI statements as analysis, not as
   authoritative facts; verify important conclusions against the cited log
   evidence.

If the Splunk `ai` command is unavailable, remove the `ai` command and replace
the final table's `ai_result_1` field with `log_text`. Keep the log-size and
candidate caps, then analyze the bounded result locally without exposing it to
an external service.
