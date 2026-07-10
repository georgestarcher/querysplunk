# querysplunk local assistant skill

Use this skill when a user asks a local AI assistant to run, inspect, or prepare `querysplunk` searches from SPL files or structured YAML configs.

## What querysplunk does

`querysplunk` runs Splunk searches from a plain SPL file or YAML config, writes the raw Splunk response body to disk, and can summarize `search.log` diagnostics for normal search jobs.

It does not store credentials in YAML. Splunk connection settings must come from environment variables, `.env`, 1Password-backed local environment files, or GitHub Actions environment secrets.

## Safe operating rules

- Never print `SPLUNKTOKEN`, `SPLUNKPASSWORD`, bearer tokens, authorization headers, or raw `.env` contents.
- Never add secrets to YAML configs.
- Warn before running a search that has no apparent `earliest` or `latest` time bounds.
- Prefer bounded searches using SPL time modifiers or YAML `dispatch.earliest_time` and `dispatch.latest_time`.
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

## Workflow for user-provided YAML

1. Check that the YAML file exists.
2. Read the YAML enough to identify `search`, `output_file`, `mode`, dispatch bounds, and diagnostics settings.
3. Confirm there are no obvious secrets in the YAML.
4. Warn if the search appears unbounded.
5. Run `querysplunk -config <file>`.
6. Read the configured `output_file` and summarize the result count and important fields.
7. If `diagnostics.search_log` is `summary`, `save`, or `both`, surface warnings and errors reported by querysplunk.
8. If `mode: export`, do not expect a search job ID or `search.log` diagnostics.

## Environment notes

If credentials are already exported in the shell, run querysplunk directly.

If using a normal `.env` file in the working directory, run:

```bash
querysplunk -e -config search.yml
```

Some 1Password local environment files are mounted as named pipes. If `querysplunk -e` cannot load that file directly, source it in the shell and then run querysplunk without `-e`:

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
