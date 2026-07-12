# querysplunk local assistant skill

Use this skill when a user asks a local AI assistant to run, inspect, or prepare `querysplunk` searches from SPL files or structured YAML configs.

## What querysplunk does

`querysplunk` runs Splunk searches from a plain SPL file or YAML config, writes the raw Splunk response body to disk, and can summarize `search.log` diagnostics for normal search jobs.

It does not store credentials in YAML. Splunk connection settings must come from environment variables, `.env`, 1Password-backed local environment files, or GitHub Actions environment secrets.

Go and AI-agent applications can use
`github.com/georgestarcher/querysplunk/v2/query` to load the same YAML, apply
the same safety policy, and execute through `splunk.Client`. Prefer this package
over reimplementing YAML or safety checks. Its zero-value policy is safe; never
use `query.UnsafeAllowAll()` without explicit user authorization.

## Safe operating rules

- Never print `SPLUNKTOKEN`, `SPLUNKPASSWORD`, bearer tokens, authorization headers, or raw `.env` contents.
- Never add secrets to YAML configs.
- Warn before running a search that has no apparent `earliest` or `latest` time bounds.
- Prefer bounded searches using SPL time modifiers or YAML `dispatch.earliest_time` and `dispatch.latest_time`.
- Expect querysplunk to block `earliest` values older than one year unless `-allow-old-earliest` or YAML `safety.allow_old_earliest` is set.
- Expect querysplunk to block explicit `index=*` searches unless `-allow-index-wildcard` or YAML `safety.allow_index_wildcard` is set.
- Treat Splunk Cloud REST endpoint restrictions as possible permission or deployment constraints, not automatically as querysplunk bugs.
- Do not run destructive or modifying SPL unless the user explicitly asks and confirms the risk.
- Summarize result files carefully; avoid dumping large raw output into chat.

## Common commands

Generate a starter YAML config:

```bash
querysplunk -write-config search.yml
```

Run a YAML config:

```bash
querysplunk -config search.yml
```

Validate a YAML config offline before execution:

```bash
querysplunk -validate-config search.yml
```

Run a plain SPL file:

```bash
querysplunk -q query.txt -o splunkresults.json
```

Add one-off dispatch bounds:

```bash
querysplunk -q query.txt -earliest=-15m -latest=now
```

Run a bundled health example:

```bash
querysplunk -config examples/health/splunkd-health.yml
```

Reconnect to an existing job without dispatching SPL:

```bash
querysplunk -job-sid <sid> -job-action status
querysplunk -job-sid <sid> -job-action wait
querysplunk -job-sid <sid> -job-action results -o results.json
querysplunk -job-sid <sid> -job-action search-log
```

Only run `querysplunk -job-sid <sid> -job-action cancel` after the user
explicitly confirms that the identified job should be cancelled. A local wait
timeout does not cancel a resumed job.

For machine-readable progress, add `-json-events` and capture standard error
separately. Parse each stderr line as one JSON event; do not merge it with raw
stdout results or `search.log`:

```bash
querysplunk -json-events -config search.yml 2>events.jsonl
```

Events contain bounded counts and state, never raw SPL, result bodies, complete
search logs, credentials, or private URLs. Summarize event kinds and outcomes
instead of dumping the complete event stream into chat.

## Workflow for user-provided YAML

1. Check that the YAML file exists.
2. Read the YAML enough to identify `search`, `output_file`, `mode`, `safety`, dispatch bounds, and diagnostics settings.
3. Confirm there are no obvious secrets in the YAML.
4. Run `querysplunk -validate-config <file>` and inspect the effective config and structured findings.
5. Resolve validation errors or ask the user to authorize any required safety acknowledgement.
6. Run `querysplunk -config <file>` only after validation succeeds.
7. Read the configured `output_file` and summarize the result count and important fields.
8. If `diagnostics.search_log` is `summary`, `save`, or `both`, surface warnings and errors reported by querysplunk.
9. If `mode: export`, do not expect a search job ID or `search.log` diagnostics.

For embedded Go workflows, use `query.LoadFile`, apply explicit
`query.Overrides`, call `query.Prepare`, inspect `Prepared.Plan` or typed
findings, and then call
`Prepared.Search`, `Prepared.SearchTo`, or `Prepared.SearchToFile`. Apply every
override before preparation so post-merge safety analysis cannot be bypassed.

For an existing SID, use `splunk.Client.InspectJob`, `WaitJob`, `JobResultsTo`,
or `JobSearchLog`. Treat SIDs as deployment-scoped references: possession does
not bypass Splunk authorization, ownership, app visibility, or retention. Never
infer permission to cancel from permission to inspect; call `CancelJob` only
after explicit user authorization.

## Environment notes

If credentials are already exported in the shell, run querysplunk directly.

If using a normal `.env` file in the working directory, run:

```bash
querysplunk -e -config search.yml
```

Some 1Password local environment files are mounted as named pipes. If
`querysplunk -e` cannot load that file directly, do not source an arbitrary
repo-local env file. Sourcing executes shell code, so only use this fallback
when the path was just created or verified through the trusted 1Password
environment tooling. Otherwise, ask the user to export the variables in their
shell or provide a normal `.env` file.

Trusted 1Password named-pipe fallback:

```bash
set -a
source ./.env.1password
set +a
querysplunk -config search.yml
```

## References

- `references/yaml-config.md`: YAML config behavior and examples.
- `references/live-integration.md`: Optional live Splunk validation workflow.
- `references/release.md`: Release bundle layout and verification.
