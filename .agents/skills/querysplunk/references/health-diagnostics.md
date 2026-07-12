# Health diagnostics playbook

Use this playbook for YAML under `examples/health/` or a user-maintained health
search derived from those examples.

## Select and validate

1. Read `examples/health/README.md` to understand the available checks and
   expected access.
2. Select only the checks relevant to the user's diagnostic question.
3. Review each YAML file for app context, time bounds, output file, and
   diagnostics before execution.
4. Run `querysplunk -validate-config <file>` for every selected file.
5. Do not broaden indexes or time ranges simply because a check returns no
   rows. Confirm permissions and deployment applicability first.

## Execute safely

- Run checks sequentially unless the user asks for controlled concurrency.
- Use `-json-events` and retain each check's events separately.
- Preserve each output file and SID so a failed local session can resume.
- Treat missing capabilities, forbidden endpoints, and empty datasets as
  deployment or permission questions before treating them as product defects.
- Never print the Splunk base URL, tenant details, credentials, private index
  names, or raw internal events.

## Interpret results

Summarize each check as:

- `healthy`: expected data is present and no material warning is indicated;
- `warning`: the check completed but reports a condition worth investigation;
- `critical`: the result or bounded diagnostics indicate likely service impact;
- `unknown`: permissions, missing data, unsupported deployment behavior, or an
  execution failure prevents a reliable conclusion.

Do not infer health solely from process exit status. Include search-log warning
and error counts, execution duration, result count, and any endpoint fallback.
Escalate `unknown` rather than repeatedly widening or rerunning the search.
