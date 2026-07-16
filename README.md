# querysplunk

`querysplunk` runs Splunk searches from SPL files or structured YAML configs. It is built for repeatable searches, offline validation, bounded diagnostics, and AI-assisted workflows that should not expose credentials or raw sensitive output.

Use it when you want to:

- run a one-off SPL file and save the Splunk response
- keep reusable searches as reviewed YAML
- validate a search plan before contacting Splunk
- collect bounded `search.log` diagnostics
- resume or inspect an existing Splunk job by SID
- let Codex or Claude Code prepare, validate, run, and summarize searches safely

For deeper guidance, use the [project wiki](https://github.com/georgestarcher/querysplunk/wiki), [INSTALL.md](INSTALL.md), the [current release notes](RELEASE_NOTES.md), and the bundled examples.

## Install

Use a prebuilt GitHub Release unless you are developing querysplunk itself. Release archives do not require Go, administrator access, or a repository clone.

1. Download the archive for your platform and `checksums.txt` from the same release.
2. Verify the checksum. See [INSTALL.md](INSTALL.md) for exact macOS, Linux, and Windows commands.
3. Extract the archive.
4. Run the bundled installer.

macOS or Linux:

```bash
./install.sh
```

Windows PowerShell:

```powershell
.\install.ps1
```

The installer places the command in a user-local bin directory, installs bundled Codex and Claude Code skills when detected, and prints PATH guidance when needed. It does not read, prompt for, copy, or configure Splunk credentials.

Verify the install:

```bash
querysplunk -version
```

Choose assistant targets explicitly when needed:

```bash
./install.sh --agent codex
./install.sh --agent claude
./install.sh --agent both
./install.sh --agent none
```

Use `./install.sh --upgrade` or `.\install.ps1 -Upgrade` for upgrades. Upgrades preserve saved YAML, results, environment files, credentials, shell settings, and unrelated assistant skills.

## First Use

Start offline. Generate a YAML file and validate the effective plan before supplying Splunk credentials:

```bash
querysplunk -write-config search.yml
querysplunk -validate-config search.yml
```

Validation checks schema, defaults, CLI overrides, and safety policy. It does not load `.env`, read credentials, or contact Splunk.

After validation, configure credentials in your environment and run the approved search:

```bash
export SPLUNKBASEURL="https://splunk.example.com:8089"
export SPLUNKTOKEN="..."
querysplunk -config search.yml
```

Use either `SPLUNKTOKEN` or `SPLUNKUSERNAME` plus `SPLUNKPASSWORD`. Optional environment settings include `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, and `SPLUNKAPP`.

## Safety Model

YAML files must not contain secrets. Keep credentials in environment variables, `.env`, 1Password-backed local env files, or GitHub environment secrets.

By default, querysplunk blocks two high-impact patterns before dispatch:

- `earliest` values older than one year
- explicit `index=*` searches

A blocked search prints a warning and stops. Acknowledgements are explicit:

```bash
querysplunk -config search.yml -allow-old-earliest
querysplunk -config search.yml -allow-index-wildcard
```

Reusable YAML can carry the same acknowledgement:

```yaml
safety:
  allow_old_earliest: true
  allow_index_wildcard: true
```

Use these only when the broader Splunk deployment impact is intentional.

## Agent Usage

Release bundles include a `querysplunk` skill for Codex and Claude Code. After installation, start a new assistant session if this is the first skill installed for that assistant.

Direct invocation:

- Codex: `$querysplunk`
- Claude Code: `/querysplunk`

Useful prompt:

```text
Use querysplunk to validate examples/health/splunkd-health.yml offline.
If validation passes, run it and summarize the result without exposing credentials,
private URLs, raw events, raw result files, or full search logs.
```

For installation through an assistant, open the extracted release directory and say:

```text
Read INSTALL.md and .agents/skills/querysplunk/SKILL.md in this extracted release.
Install querysplunk for my available local assistants using the bundled installer.
Do not configure or display Splunk credentials. Verify the installed command and skill files when finished.
```

The agent should validate YAML before live execution, keep credentials out of YAML and chat, preserve safety controls, use `-json-events` for machine-readable progress, resume existing jobs by SID when possible, and require explicit approval before cancellation.

## Common Commands

```bash
# Version and install check
querysplunk -version

# Generate and validate YAML offline
querysplunk -write-config search.yml
querysplunk -validate-config search.yml

# Run YAML or SPL
querysplunk -config search.yml
querysplunk -q query.txt -o splunkresults.json
querysplunk -q query.txt -earliest=-15m -latest=now

# Capture machine-readable progress on stderr
querysplunk -json-events -config search.yml 2>events.jsonl

# Resume an existing job without redispatching SPL
querysplunk -job-sid 1258421375.19 -job-action status
querysplunk -job-sid 1258421375.19 -job-action wait
querysplunk -job-sid 1258421375.19 -job-action results -o results.json
querysplunk -job-sid 1258421375.19 -job-action search-log
```

Run `querysplunk -h` for the full CLI reference.

## YAML Searches

Generated and bundled YAML uses `schema_version: "1"`. Existing runtime-only
YAML remains valid and is interpreted as version 1. Bundled searches add
`metadata`, `requirements`, `provenance`, `interpretation`, `result_handling`,
and `result_contract` blocks so people, agents, and Go consumers can select and
handle them consistently.

A minimal runtime config:

```yaml
schema_version: "1"

app: search
output_file: splunkresults.json
mode: job
search: |
  search index=_internal earliest=-15m
  | head 1

dispatch:
  earliest_time: "-15m"
  latest_time: "now"

results:
  endpoint: auto
  output_mode: json
  count: 0

safety:
  allow_old_earliest: false
  allow_index_wildcard: false

diagnostics:
  search_log: summary
```

Bundled examples live under `examples/`. Health checks are in `examples/health/` and include notes about required Splunk access and Splunk Cloud caveats.

## Bundled Search Library

Release archives include reviewed YAML families for different operating
contexts:

- `examples/health/` contains bounded deployment, scheduler, message, orphaned
  search, and audit-based health checks.
- `examples/rest/` contains read-only saved-search, macro, and lookup
  inspection workflows.
- `examples/detections/` contains defender searches, Splunk AI telemetry
  research, and the experimental AI-agent detection starter pack.
- `examples/pentest/` contains explicitly authorized security-assessment
  searches whose outputs can contain credentials and must not be displayed by
  an assistant.

Validate a bundled search offline before execution. Respect its
`result_handling` classification and do not weaken its `result_contract` merely
to force a result through.

## Splunk MCP Server

Splunk MCP Server and querysplunk are complementary. MCP is useful for interactive discovery and supported MCP operations. querysplunk is useful for repeatable YAML libraries, CI, long-running jobs, saved output, SID recovery, and bounded `search.log` diagnostics.

A practical assistant workflow is to explore through MCP, refine the SPL, then save recurring searches as querysplunk YAML.

## Go Packages

Applications can use the same YAML schema and safety checks directly:

```go
import "github.com/georgestarcher/querysplunk/v2/query"
```

Use `query.LoadFile`, apply `query.Overrides`, call `query.Prepare`, inspect the credential-free plan and findings, then execute through the prepared query. The lower-level Splunk client is available at:

```go
import "github.com/georgestarcher/querysplunk/v2/splunk"
```

Prefer the `query` package when consumers should share CLI YAML validation and safety controls. Use the lower-level `splunk` package only when your application owns policy enforcement.

## Build From Source

Building from source requires Go 1.26+. Release archives do not require Go.

```bash
go build -o querysplunk .
go test ./...
```

## Public Issues

This is a public repository. Use the issue templates for bug reports, feature requests, and proposed YAML saved searches. Remove Splunk credentials, private URLs, tenant names, private index names, sensitive SPL, customer data, and deployment-specific details before submitting.

Proposed reusable YAML searches should use the saved-search review template and include time bounds, expected access requirements, and Splunk deployment impact.

## More Documentation

- [Current release notes](RELEASE_NOTES.md)
- [Installation and upgrades](INSTALL.md)
- [Health examples](examples/health/README.md)
- [Defender detection examples](examples/detections/README.md)
- [Authorized penetration-testing examples](examples/pentest/README.md)
- [Project wiki](https://github.com/georgestarcher/querysplunk/wiki)
- [AI-agent detections wiki](https://github.com/georgestarcher/querysplunk/wiki/AI-Agent-Detections)
