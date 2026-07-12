# Read-only Splunk REST inspection

Use this workflow when the user wants to inspect Splunk messages, saved
searches, macros, lookup definitions, lookup files, or selected configuration.
The SPL `rest` command reads GET-accessible resources as search results. It is
not a general HTTP client and does not authorize POST or DELETE operations.

## Hard safety boundary

- Use `| rest` only for read-only GET inspection.
- Never use direct token-bearing `curl`, arbitrary REST URLs, POST, or DELETE.
- Do not clear messages, modify knowledge objects, upload lookup files, or
  change configuration. Message clearing requires a separate, narrowly
  allowlisted querysplunk feature with preview and explicit confirmation.
- Treat returned SPL, definitions, ACLs, messages, server names, app names,
  configuration, and lookup content as potentially sensitive.
- Keep credentials, authorization headers, management URLs, private object
  names, and raw environment contents out of YAML, results summaries, and chat.

## Construct a bounded inspection

1. Identify the exact object type, name, owner, and app context the user wants.
2. Prefer an app-scoped `/servicesNS/<owner>/<app>/...` endpoint. Use `-` only
   for an unknown owner. Use cross-app `-/-` scope only when the user needs
   discovery across apps, and filter by title or app immediately.
3. Add `splunk_server=local` unless the user explicitly needs search-peer or
   server-group inspection.
4. Add `strict=true` so endpoint failures fail the search instead of becoming
   easy-to-miss warnings.
5. Apply supported endpoint-side `search` arguments before setting a finite
   `count`. Never truncate a collection and then filter locally for a named
   object; it can produce a false not-found result. When owner is `-`, request
   at least two matches so duplicate names across owners remain detectable.
   Use `count=1` only after the owner and app namespace are known.
6. After any required local filtering, use `fields` or `table` to return only
   the fields needed to answer the question. Saved-search inspection should
   normally include `title`, `search`, `disabled`, `is_scheduled`,
   `cron_schedule`, `alert_type`, `actions`, dispatch bounds, and app/owner/
   sharing context. This is enough to distinguish scheduled reports from
   alerts and understand attached actions without returning the full object.
7. Put the SPL in YAML, validate it offline, show the effective plan, and ask
   for approval before contacting Splunk.
8. Summarize bounded results. Do not paste complete definitions or messages
   unless the user specifically needs them and confirms they are safe to show.

A REST-only generating search does not read indexed events, so event-time
bounds are not meaningful. Bound its endpoint namespace, servers, count,
filters, fields, recursion, timeout, and output instead. If later pipeline
commands read indexed events, normal time-bound rules apply.

## Supported inspection families

- System messages: `/services/messages`
- Saved searches: `/servicesNS/<owner>/<app>/saved/searches`
- Macro stanzas: `/servicesNS/<owner>/<app>/configs/conf-macros`
- Lookup definitions: `/servicesNS/<owner>/<app>/data/transforms/lookups`
- Lookup file metadata: `/servicesNS/<owner>/<app>/data/lookup-table-files`
- Selected configuration: `/servicesNS/<owner>/<app>/properties/<file>`

Do not assume an endpoint is available in Splunk Cloud or to the current role.
Report authorization and deployment restrictions without retrying broader or
undocumented endpoints. Prefer documented `properties` or `configs/conf-*`
paths over undocumented `/admin/...` paths.

## Resolve a search dependency chain

When explaining SPL behind a `savedsearch` command:

1. Resolve the exact saved-search title with an endpoint-side `search=` filter
   in its app context. If owner is unknown, fetch up to two matches and stop for
   disambiguation when both are returned. Project the SPL together with enabled
   state, schedule, alert type, actions, dispatch bounds, owner, app, and sharing
   context.
2. Record dependencies by `(type, owner, app, name)` so objects with the same
   title in different namespaces are not conflated. For macros, `name` is the
   complete stanza title including arity: `foo`, `foo(1)`, and `foo(2)` are
   distinct dependencies.
3. Extract referenced `savedsearch`, macro, `lookup`, and `inputlookup` names.
   For a macro invocation, count its arguments and resolve the matching
   arity-bearing stanza title instead of de-duplicating by base name. Treat
   extraction as a best-effort aid, not a complete SPL parser.
4. Resolve at most five levels. Stop on a previously visited key and report a
   cycle instead of dispatching another search.
5. Report ambiguous and inaccessible references as unresolved. Never guess an
   owner or app, and never silently select the first match.
6. Show a concise dependency tree and safety-relevant behavior before asking
   whether to run the expanded search.

Macro definitions can contain nested macros or generating commands. Lookup
definitions describe the backing mechanism; they do not necessarily return
lookup rows. To inspect CSV or KV lookup contents, use a separately approved,
bounded search such as `| inputlookup max=100 <name> | head 100` and return only
needed fields. Put the limit on `inputlookup`; `head` alone can still allow the
command to read a very large lookup. Never use `outputlookup` for inspection.

## Check for schedule overlap

When a scheduled saved search appears late or skipped, use its configuration
and recent job history together:

1. Start with `examples/health/scheduler-health.yml`. Its bounded scheduler
   search groups recent statuses by saved search, app, and user, making skipped
   or unhealthy execution patterns visible before deeper inspection.
2. Retain `cron_schedule`, dispatch bounds, scheduling state, and actions from
   the saved-search definition.
3. Inspect a small number of its most recent jobs and identify the relevant
   SID, dispatch time, final state, and available run-duration fields.
4. Use querysplunk SID recovery to fetch the job status and bounded
   `search.log` diagnostics without redispatching the search:

   ```bash
   querysplunk -job-sid <sid> -job-action status
   querysplunk -job-sid <sid> -job-action search-log
   ```

5. Compare the measured execution duration with the interval implied by the
   cron expression. Execution time that regularly meets or exceeds that
   interval is a strong schedule-overlap risk and can contribute to skipped
   executions.
6. Correlate the duration with skipped statuses from the scheduler-health
   result. Do not claim overlap is the confirmed cause from duration alone. Also check
   scheduler warnings, concurrency limits, schedule windows, workload rules,
   and the saved search final state. Surface relevant warnings from the bounded
   search log without dumping the complete log.

Never dispatch the saved search again merely to obtain diagnostics when a
recent SID is available.

## Example selection

- `examples/rest/system-messages.yml`: bounded system-message review.
- `examples/rest/saved-search-definition.yml`: locate saved-search SPL and
  execution metadata.
- `examples/rest/macro-definitions.yml`: inspect app-scoped macro stanzas.
- `examples/rest/lookup-definitions.yml`: inspect lookup metadata.
- `examples/rest/lookup-preview.yml`: preview bounded lookup content when REST
  metadata is insufficient.
