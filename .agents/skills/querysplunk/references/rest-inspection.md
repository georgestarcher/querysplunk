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
5. Set a finite `count`, apply supported endpoint-side `search` arguments when
   possible, then retain only the fields needed to answer the question.
6. Put the SPL in YAML, validate it offline, show the effective plan, and ask
   for approval before contacting Splunk.
7. Summarize bounded results. Do not paste complete definitions or messages
   unless the user specifically needs them and confirms they are safe to show.

A REST-only generating search does not read indexed events, so event-time
bounds are not meaningful. Bound its endpoint namespace, servers, count,
filters, fields, recursion, timeout, and output instead. If later pipeline
commands read indexed events, normal time-bound rules apply.

## Supported inspection families

- System messages: `/services/messages`
- Saved searches: `/servicesNS/<owner>/<app>/saved/searches`
- Macro stanzas: `/servicesNS/<owner>/<app>/properties/macros`
- Lookup definitions: `/servicesNS/<owner>/<app>/data/transforms/lookups`
- Lookup file metadata: `/servicesNS/<owner>/<app>/data/lookup-table-files`
- Selected configuration: `/servicesNS/<owner>/<app>/properties/<file>`

Do not assume an endpoint is available in Splunk Cloud or to the current role.
Report authorization and deployment restrictions without retrying broader or
undocumented endpoints. Prefer documented `properties` or `configs/conf-*`
paths over undocumented `/admin/...` paths.

## Resolve a search dependency chain

When explaining SPL behind a `savedsearch` command:

1. Resolve the exact saved-search title in its app context and retain the
   returned owner, app, sharing, disabled state, and SPL.
2. Record dependencies by `(type, owner, app, name)` so objects with the same
   title in different namespaces are not conflated.
3. Extract referenced `savedsearch`, macro, `lookup`, and `inputlookup` names.
   Treat extraction as a best-effort aid, not a complete SPL parser.
4. Resolve at most five levels. Stop on a previously visited key and report a
   cycle instead of dispatching another search.
5. Report ambiguous and inaccessible references as unresolved. Never guess an
   owner or app, and never silently select the first match.
6. Show a concise dependency tree and safety-relevant behavior before asking
   whether to run the expanded search.

Macro definitions can contain nested macros or generating commands. Lookup
definitions describe the backing mechanism; they do not necessarily return
lookup rows. To inspect CSV or KV lookup contents, use a separately approved,
bounded search such as `| inputlookup <name> | head 100` and return only needed
fields. Never use `outputlookup` for inspection.

## Example selection

- `examples/rest/system-messages.yml`: bounded system-message review.
- `examples/rest/saved-search-definition.yml`: locate saved-search SPL and
  execution metadata.
- `examples/rest/macro-definitions.yml`: inspect app-scoped macro stanzas.
- `examples/rest/lookup-definitions.yml`: inspect lookup metadata.
- `examples/rest/lookup-preview.yml`: preview bounded lookup content when REST
  metadata is insufficient.
