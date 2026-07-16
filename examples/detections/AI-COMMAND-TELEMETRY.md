# Splunk AI command telemetry mapping

This document records the telemetry contract that querysplunk can use for
future detections involving the Splunk `ai` search command. It is a research
result, not a claim that every Splunk deployment exposes the same fields.

## Scope and method

The observations were made on July 16, 2026, against one Splunk Cloud Platform
10.4 deployment with Splunk AI Assistant 2.1.1. The review used read-only,
bounded searches plus two synthetic probes:

- A successful `makeresults` pipeline sent only synthetic text to the `ai`
  command and confirmed that an AI result reached the search pipeline.
- A deliberately unsupported `ai` argument safely exercised a failed command
  path without sending customer data or changing configuration.

No application, logging, model, or Splunk configuration was changed. Recheck
the field inventory after an AI Assistant or Splunk platform upgrade.

The Splunk `ai` command is a transforming search command. It enriches or
extracts features from rows in the current pipeline and returns fields such as
`ai_result_1`; it does not independently email, write, run scripts, dispatch
searches, or invoke external tools. Any action claim requires evidence of a
separate action-capable command, and command co-occurrence alone does not prove
that the action consumed AI output.

## Observed telemetry surfaces

| Surface | Availability | Useful fields | Limits |
| --- | --- | --- | --- |
| `_audit` search activity | Primary and consistently populated for observed `ai` searches | `_time`, `user`, `app`, `action`, `search_type`, `info`, `search_id`, `search` | `search` can contain the complete SPL and prompt. Terminal records did not expose separate `status`, `reason`, or `error` fields. |
| `_internal` `splunk_ai_assistant.log*` | Available in the observed Splunk Cloud deployment | `message`, `level`, `user`, `chat_id`, `request_id`, `metadata.request_id`, `metadata.search`, `metadata.spl_name`, `source_app`, `status` | Most AI-specific fields are conditional. Messages, searches, prompts, users, tenant identifiers, and object names can be sensitive. |
| `_internal` extension-platform worker log | Available in the observed Splunk Cloud deployment | `traceid`, `spanid`, `app.name`, `command`, `execution.mode`, `error` | `error` is conditional. No structured prompt, response, model, token, or duration field was observed. |
| Search job `search.log` | Available while the job remains retained and authorized | Parser, command, authorization, remote-command, warning, error, and execution diagnostics | Retrieval requires the canonical SID and access to `/services/search/jobs/{sid}/search.log`. Expired jobs return `Unknown sid`. A completed job can still contain warning and error lines. |
| Search-command registration REST data | Not reliably observable in this deployment | None established | The configured MCP search tool rejects the `rest` command, and the Splunk Cloud role did not return a dependable command-registration inventory. Successful execution proves that `ai` is registered, but not how it is configured. |

Splunk documents `_audit` as the platform audit source for searches and other
user activity. It documents `search.log` as a search-job REST resource. Splunk
Cloud REST access is limited to the search tier and normally requires port
`8089` access and an appropriate IP allow list.

## ATR-oriented field contract

The normalized names below follow the concepts proposed for the querysplunk
Agent Threat Rules-inspired starter pack. Availability means availability in
the observed deployment, not a guarantee across versions.

| ATR concept | Primary mapping | Availability | Guidance |
| --- | --- | --- | --- |
| `session.id` | Trim surrounding apostrophes from `_audit.search_id` | Consistent in matching audit events | This is a Splunk search-job identifier. Use it for job and `search.log` operations only. |
| `user.name` | `_audit.user` | Consistent in matching audit events | Internal-log `user` is conditional and can enrich, but not replace, the audit actor. |
| `agent.name` | Constant `splunk_ai_assistant`, inferred from the command and log source | Inferred | The source did not provide a stable normalized agent-name field. |
| `agent.model` | None | Unavailable | Do not infer a configured or selected model from app version, response text, or timing. |
| `tool.name` | Constant `ai` after matching an actual `ai` command in SPL | Inferred | The worker-log `command` field is conditional supporting evidence. |
| `tool.action` | Constant `execute`; preserve `_audit.action` and `search_type` separately | Inferred plus consistent context | The observed audit values described an ad hoc search, not the model operation itself. |
| `tool.input` | `_audit.search`; conditionally `metadata.search` | Available but sensitive | Default detections should return length and, only when needed, a SHA-256 correlation value instead of raw SPL. A hash is pseudonymous, not anonymous. |
| `tool.output` | None | Unavailable | AI output was present in live search results but was not observed as a durable structured audit or internal-log field. |
| `tool.status` | `_audit.info` | Consistent in matching audit events | Observed values included `granted`, `completed`, `bad_request`, and `failed`. |
| `tool.error` | Search job `search.log`; conditionally worker-log `error` | Conditional | `_audit` did not expose a separate reason or error field for terminal records. |
| `tool.duration_ms` | None | Unavailable as model-call duration | An audit event span can approximate search activity elapsed time, but it is not reliable model latency. Search-log execution timing also must not be relabeled as model latency. |
| `target.type` | Constant `splunk_search_job` | Inferred | Internal AI app events can instead use `splunk_ai_request` when they are not tied to a search job. |
| `target.name` | Normalized audit `search_id`; conditionally `metadata.spl_name` for internal events | Consistent for jobs, conditional for named searches | Do not treat a prompt or raw SPL as a target name. |
| `source.type` | `splunk_audit`, `splunk_internal`, or `splunk_search_log` | Inferred | Preserve the event family because its identifiers have different meanings. |
| `source.name` | `sourcetype` or the recognized internal-log family | Consistent or inferred | Avoid returning tenant-specific filesystem paths when a generic family name is sufficient. |

## Correlation and timing limits

The observed audit records consistently included `search_id`. The internal AI
logs offered `request_id`, `metadata.request_id`, `chat_id`, `traceid`, and
`spanid`, but a bounded 24-hour comparison found no shared value between six
distinct audit job identifiers and 59 distinct internal identifiers. Do not
join those fields or coalesce them into one cross-source session identifier.

Keep separate correlation domains:

- Use normalized `_audit.search_id` for the Splunk job and its `search.log`.
- Use `request_id` or `metadata.request_id` only within the corresponding AI
  Assistant log family.
- Use `chat_id` only as conversation context.
- Use `traceid` and `spanid` only for extension-platform worker traces.

Audit timestamps can measure the span between records that happen to share a
job identifier. The live sample produced plausible elapsed values for some
completed and bad-request jobs, but other terminal or nonterminal identifiers
had a zero span and granted records used a separate identifier domain. This is
not a dependable `tool.duration_ms` source.

## Successful and failed path evidence

The successful synthetic probe returned one result row with AI output present
and ended successfully. Its `search.log` still produced both warning and error
diagnostics. A detection must not treat the presence of an error-level log line
as proof that the command failed.

The safe failed probe used an unsupported argument. querysplunk returned a
nonzero operation result, Splunk created a job, and the immediately retrieved
`search.log` contained parser and external-command error evidence. This is the
preferred failure pattern: use `_audit.info` or the job terminal state to find a
candidate, then use the retained job log to determine cause.

One older audit failure referenced a subsearch SID whose job had expired. The
REST endpoint correctly returned `Unknown sid`. Rules and assistants must
report the log as unavailable rather than redispatching the original search or
claiming that no error occurred.

## Tested bounded normalization SPL

This candidate provides the primary event stream for the starter pack. Run it
with dispatch bounds such as `earliest_time: -24h` and `latest_time: now`. It
caps output at 200 records and deliberately omits raw SPL.

```spl
search index=_audit action=search
| eval ai_command_pattern="\\|\\s*"."a"."i"."\\s+"
| where match(search, ai_command_pattern)
| eval session_id=trim(search_id, "'"),
    user_name=user,
    agent_name="splunk_ai_assistant",
    tool_name="ai",
    tool_action="execute",
    tool_status=info,
    tool_input_sha256=sha256(search),
    tool_input_length=len(search),
    target_type="splunk_search_job",
    target_name=trim(search_id, "'"),
    source_type="splunk_audit",
    source_name=sourcetype
| sort 0 -_time
| head 200
| table _time, session_id, user_name, app, search_type, agent_name, tool_name, tool_action, tool_status, tool_input_sha256, tool_input_length, target_type, target_name, source_type, source_name
```

The command pattern is assembled at search time so the audit search does not
match itself merely because its own SPL contains a literal `| ai` example.
`tool_input_sha256` supports equality correlation without returning raw SPL,
but low-entropy input can still be guessed and rehashed. Classify this output
as sensitive.

For a failed or bad-request record, use the normalized `session_id` with:

```bash
querysplunk -job-sid <sid> -job-action search-log -o search.log
```

Limit follow-up retrieval to a small number of recent jobs. Preserve the job's
original authorization and retention behavior. Do not use `map` to fan out an
unbounded number of REST calls, and do not redispatch saved searches only to
recreate missing logs.

## Sensitivity and retention

In the seven-day sample, every matching audit event contained the `search`
field and most contained a prompt marker. The internal AI log also exposed
conditional search and SPL-name fields. Treat all of the following as
sensitive:

- Full `_audit.search` and `metadata.search` values.
- Prompts and AI responses.
- User, chat, request, trace, span, tenant, app, and job identifiers.
- Internal `message` and `error` values.
- Full `search.log` text and any correlation hash derived from SPL.

Default saved searches should use `result_handling.classification: sensitive`,
`agent_display: summary_only`, file mode `0600`, temporary retention, explicit
time bounds, and a row cap. Never put raw live values in public issues, tests,
fixtures, or documentation.

## Splunk Cloud and Enterprise distinctions

The indexed source names and extracted fields above are observations from one
Splunk Cloud deployment. Splunk AI Assistant runs as a separate cloud-connected
service, and its app and worker logging can change independently of the Splunk
platform. Splunk Enterprise can have a different app version, no AI Assistant,
different log forwarding, or different role visibility.

The `_audit`-first candidate is the portable baseline because Splunk documents
search activity as audit data. Internal app-log enrichment must be optional and
must degrade cleanly when its source or fields are absent. Search-log follow-up
requires a retained, authorized job. In Splunk Cloud, the client must also have
permitted search-tier REST access; other platform tiers remain provider
managed.

## Recommendation for the starter pack

The native telemetry is sufficient to proceed with a conservative starter pack
in issue #67 for:

- AI command execution inventory by actor, app, outcome, and search type.
- Failed and bad-request AI command activity with bounded `search.log`
  follow-up.
- Unusual volume, actor, app-context, or status patterns.
- Sensitive AI input indicators derived from audited SPL without returning raw
  prompts by default.
- Optional AI Assistant internal-log error and request enrichment.

The telemetry is not sufficient for rules that claim to identify model choice,
AI output content, token consumption, exact model latency, or a guaranteed
cross-source conversation. Those rules must remain out of scope until a stable
source is observed and documented.

## Official references

- [Splunk Audit Logs data model](https://help.splunk.com/en/splunk-cloud-platform/common-information-model/8.5/data-models/splunk-audit-logs)
- [Auditing activities in a Splunk platform instance](https://help.splunk.com/?resourceId=Splunk_Security_cd341927d-9ee7-4082-b6ca-ccfb72844310)
- [Search endpoint descriptions](https://help.splunk.com/en/splunk-enterprise/leverage-rest-apis/rest-api-reference/10.4/search-endpoints/search-endpoint-descriptions)
- [Splunk Cloud REST API access requirements and limitations](https://help.splunk.com/splunk-enterprise/leverage-rest-apis/rest-api-tutorials/9.0/rest-api-tutorials/access-requirements-and-limitations-for-the-splunk-cloud-platform-rest-api)
- [About Splunk AI Assistant](https://help.splunk.com/en/splunk-cloud-platform/search/splunk-ai-assistant-for-spl)
- [Use Splunk AI Assistant 2.1](https://help.splunk.com/en/splunk-cloud-platform/search/splunk-ai-assistant/2.1.0/use-splunk-ai-assistant/use-splunk-ai-assistant)
