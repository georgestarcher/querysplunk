# Preflight and recovery playbook

Use this playbook before live execution and whenever a search does not complete
cleanly. The goal is to diagnose the current state, not to repeat commands until
one succeeds.

## Preflight

1. Run `querysplunk -version` and confirm the installed release is usable.
2. Confirm the intended YAML or SPL file exists.
3. For YAML, run `querysplunk -validate-config <file>` before loading
   credentials or contacting Splunk.
4. Inspect the effective app, time bounds, mode, output path, result settings,
   diagnostics, warnings, acknowledgements, and blocking findings.
5. Confirm the output directory exists and is writable. Do not overwrite an
   unrelated file without approval.
6. Check only whether required environment variable names are present. Never
   print their values or dump the environment. Token authentication normally
   requires `SPLUNKBASEURL` and `SPLUNKTOKEN`.
7. Ask for explicit authorization before adding a safety acknowledgement,
   running modifying SPL, or cancelling a job.
8. For live work, add `-json-events` and keep stderr events separate from result
   output. Record the SID as soon as `job_dispatched` reports it.

## Failure classification

| Failure | Required response |
| --- | --- |
| YAML/schema/safety validation | Fix locally and validate again. Do not contact Splunk. |
| Missing credential variable | Name the missing variable only and stop for user correction. |
| HTTP 401 | Stop. Ask the user to refresh or correct authentication. Do not retry. |
| HTTP 403 | Stop. Explain that app, index, ownership, role, or endpoint permission may be missing. |
| TLS validation | Stop. Never disable TLS verification automatically. |
| Network failure before a SID | Allow at most one bounded retry when the failure appears transient. |
| Network or local timeout after a SID | Inspect or wait on that SID. Never redispatch merely because the local command ended. |
| Terminal failed job | Fetch bounded search-log diagnostics before proposing a corrected search. |
| Completed job with local output failure | Retrieve results again from the same SID after fixing the local path. Do not rerun SPL. |
| v2 result endpoint unavailable | Let querysplunk perform its supported fallback. Do not construct ad hoc REST calls. |
| Search completed with warnings/errors | Surface bounded diagnostics even when the job state itself is successful. |
| Modifying or unusually broad SPL | Stop for explicit user authorization. |

## Retry limits

- Never retry validation, authentication, authorization, TLS, or safety errors.
- Allow at most one retry for a clearly transient connection failure before a
  SID is known.
- Once a SID exists, use `status`, `wait`, `results`, or `search-log` against
  that SID instead of dispatching another search.
- Do not cancel a remote job because a local context, terminal, or assistant
  session ended.

## Resume sequence

```bash
querysplunk -json-events -job-sid <sid> -job-action status
querysplunk -json-events -job-sid <sid> -job-action wait
querysplunk -json-events -job-sid <sid> -job-action results -o <output-file>
querysplunk -json-events -job-sid <sid> -job-action search-log
```

Use the handoff template under `templates/handoff.yml` when another session may
need to continue. Do not include credentials, base URLs, raw SPL, raw results,
or raw search-log lines.
