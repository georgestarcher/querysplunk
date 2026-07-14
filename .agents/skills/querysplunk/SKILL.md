---
name: querysplunk
description: Safely install, upgrade, prepare, validate, execute, monitor, resume, and inspect Splunk searches, scheduled-search failures, long-running and orphaned scheduled searches, knowledge objects, messages, macros, lookups, and configuration with querysplunk YAML files or SPL input.
---

# querysplunk local assistant skill

Use this skill when a user asks a local AI assistant to install or upgrade
querysplunk, or to prepare, validate, run, monitor, inspect, or resume searches
from SPL files or structured YAML configs. Also use it to inspect system
messages, expand saved searches, resolve macros or lookups, and explain
read-only Splunk REST dependencies.

## Installation and upgrades

When working from an extracted release bundle, read the bundled `INSTALL.md`
and use `install.sh` on macOS/Linux or `install.ps1` on Windows. Do not recreate
the installer by manually copying files. The installer can target Codex, Claude
Code, both, or neither and never reads Splunk credentials.

Use the installer's explicit upgrade mode for a different installed version.
Preserve saved YAML, results, environment files, credentials, configuration,
shell settings, and unrelated skills. Never authorize a downgrade unless the
user explicitly requests it. Verify `querysplunk -version` and the selected
assistant skill files after installation or upgrade.

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
- Before constructing a `| rest` search or resolving a saved-search, macro, lookup, or configuration dependency, read `references/rest-inspection.md`.
- Treat `| rest` as read-only GET inspection. Never improvise POST, DELETE, direct token-bearing `curl`, or arbitrary REST URLs.
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

Before live execution, read `references/preflight-and-recovery.md`. When
creating or materially changing SPL, also read `references/spl-authoring.md`.

1. Check that the YAML file exists.
2. Read the YAML enough to identify `search`, `output_file`, `mode`, `safety`, dispatch bounds, and diagnostics settings.
3. Confirm there are no obvious secrets in the YAML.
4. Run `querysplunk -validate-config <file>` and inspect the effective config and structured findings.
5. Resolve validation errors or ask the user to authorize any required safety acknowledgement.
6. Run `querysplunk -config <file>` only after validation succeeds.
7. Read the configured `output_file` and summarize the result count and important fields.
8. If `diagnostics.search_log` is `summary`, `save`, or `both`, surface warnings and errors reported by querysplunk.
9. If `mode: export`, do not expect a search job ID or `search.log` diagnostics.

After results are saved, follow `references/result-analysis.md`. For bundled or
user-maintained health checks, follow `references/health-diagnostics.md`. If a
job may outlive the current session, record only non-sensitive state using
`templates/handoff.yml`.

When a user asks to check a saved search for recent job failures, follow
`references/scheduler-job-failures.md`. Use the exact saved-search name and do
not infer success from an empty scheduler history.

When a user asks to find failed searches with available logs or profile
successful searches that may overrun their schedules, follow
`references/scheduled-search-log-analysis.md`. Prefer retained SIDs over
redispatching searches, preserve the templates' analysis caps, and distinguish
measured log evidence from AI inference.

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
- `references/installation.md`: Release installation and safe upgrade workflow.
- `references/preflight-and-recovery.md`: Deterministic checks, failure classification, retry limits, and SID recovery.
- `references/spl-authoring.md`: Time-bounded SPL authoring and modifying-command safety.
- `references/result-analysis.md`: Bounded, privacy-aware result and diagnostic summaries.
- `references/health-diagnostics.md`: Safe selection, execution, and interpretation of health checks.
- `references/scheduler-job-failures.md`: Check one saved search across all apps for recent failed, skipped, or otherwise non-successful scheduler runs.
- `references/scheduled-search-log-analysis.md`: Profile long-running successful jobs and diagnose failed jobs whose `search.log` is retained.
- `references/rest-inspection.md`: Read-only REST endpoint selection, namespacing, dependency expansion, and mutation boundaries.
- `templates/recent-search-job-failures.yml`: Analyze recent failed scheduled jobs whose `search.log` is retained.
- `templates/long-running-successful-searches.yml`: Profile successful scheduled jobs that approach or exceed their observed schedule gap.
- `templates/handoff.yml`: Non-sensitive state for continuing work in another session.
