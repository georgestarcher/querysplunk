# SPL authoring and safety playbook

Use this playbook when creating or materially changing a querysplunk YAML or SPL
file.

## Authoring order

1. Generate YAML with `querysplunk -write-config <file>` instead of inventing
   fields.
2. State the user's question and the expected result shape before writing SPL.
3. Select the narrowest appropriate app, indexes, sourcetypes, hosts, and
   fields.
4. Add explicit `earliest` and `latest` bounds in SPL or dispatch settings.
5. Start with a small result limit or aggregation while developing the search.
6. Configure output and diagnostics intentionally.
7. Validate offline and show the effective plan before live execution.

## Safety rules

- Never add `allow_old_earliest` or `allow_index_wildcard` merely to make
  validation pass. Explain the finding and request explicit authorization.
- Treat commands that write, delete, collect, alert, or alter knowledge objects
  as modifying operations. Examples include `collect`, `delete`,
  `outputlookup`, alert actions, and REST-driven modifications.
- Do not run modifying SPL unless the user explicitly requested the action,
  understands the target and scope, and confirms execution.
- Prefer known indexes over `index=*` and bounded windows over all time.
- Do not assume the default `search` app can see every knowledge object. Record
  the intended app context in YAML.
- Keep credentials, private URLs, and authorization material out of SPL and
  YAML.

## Incremental development

For an expensive or unfamiliar query:

1. Confirm the base dataset over a short window.
2. Add filters before transforms.
3. Add one transform or aggregation at a time.
4. Validate field names with a small sample.
5. Increase the time range or result count only after the bounded form works.

Do not repeatedly dispatch variants when one existing SID can provide status,
results, or search-log diagnostics.
