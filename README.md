# querysplunk

`querysplunk` runs Splunk searches from a plain SPL file or a structured YAML
config, writes the raw Splunk response body to disk, and can summarize
`search.log` diagnostics for completed jobs.

## Requirements

Go 1.26+ is required.

## Dependencies

If you build from source, Go resolves these dependencies automatically:

- https://github.com/joho/godotenv
- https://gopkg.in/yaml.v3

## Quick setup

```bash
brew install go
# or upgrade if already installed
brew upgrade go
```

Build the CLI locally:

```bash
go build -o querysplunk .
```

## Configuration environment

By default, the tool reads connection settings from operating system
environment variables. Use `-e` to load a file named `.env` from the current
working directory before reading the environment.

```text
SPLUNKUSERNAME=
SPLUNKPASSWORD=
SPLUNKBASEURL=
SPLUNKTOKEN=
SPLUNKTIMEOUT=120
SPLUNKTLSVERIFY=true
SPLUNKAPP=
```

- Use either a Splunk token or username/password credentials. If `SPLUNKTOKEN`
  is set, token authentication is used and credentials are ignored.
- `SPLUNKBASEURL` should point to the Splunk management API, typically ending
  in port `8089`.
- `SPLUNKTLSVERIFY=false` disables Splunk TLS certificate validation. If unset,
  TLS verification defaults to `true`.
- `SPLUNKTIMEOUT` defaults to `120` seconds. This is the maximum time the tool
  waits for a dispatched search job to reach `DONE`. If the local timeout
  expires after a job has been created, the tool attempts to cancel the remote
  Splunk job before exiting.
- Use `SPLUNKAPP` or `-app` to scope the search to a Splunk app namespace.

## SPL query file

The tool reads the SPL search from a file. By default, it reads `query.txt`.
Use `-q` to provide a different file, such as `investigation.spl`.

For complex investigations, consider keeping the SPL in Splunk as a saved
search and calling it from the query file:

```
savedsearch "SOAR - Auth Model - Investigation" user=bob
```

This pattern works well from SOAR products or Splunk Enterprise Security
correlation search drilldown fields. It keeps SPL complexity, permissions, and
documentation in Splunk while the CLI only passes runtime arguments.

## Structured YAML search config

Plain SPL files remain supported. Use `-config` when a search needs reusable
settings beyond the SPL text, such as app context, output file, execution mode,
dispatch parameters, result parameters, or search log diagnostics.

```bash
querysplunk -config search.yml
```

### Generate a YAML skeleton

Use `-write-config` to create a starter YAML config file. This is the easiest
way to see the supported YAML shape without copying an example by hand:

```bash
querysplunk -write-config search.yml
```

The generated file includes placeholders for app context, output file, search
text, dispatch parameters, result parameters, execution mode, and diagnostics.
It does not include secrets.

The command refuses to overwrite an existing file unless you also pass
`-force`:

```bash
querysplunk -write-config search.yml -force
```

### YAML config example

For quick one-off bounds without YAML, use dispatch-level time flags:

```bash
querysplunk -q query.txt -earliest=-15m -latest=now
```

If neither the SPL nor dispatch parameters include `earliest` / `latest` time
bounds, the tool logs a warning. Existing unbounded searches still run, but
Splunk REST searches can otherwise run over all time.

By default, the tool blocks two high-impact search patterns before dispatch:

- `earliest` values older than one year, whether supplied in SPL, YAML
  `dispatch.earliest_time`, or the `-earliest` flag
- explicit `index=*` searches

These controls print a warning and stop the search. Use the override flags or
YAML `safety` fields only when you intend the broader Splunk deployment impact:

```bash
querysplunk -q query.txt -allow-old-earliest
querysplunk -q query.txt -allow-index-wildcard
```

For reusable YAML searches, set the acknowledgement with the search:

```yaml
safety:
  allow_old_earliest: true
  allow_index_wildcard: true
```

Example:

```yaml
app: search
output_file: splunkresults.json
mode: job
search: |
  search index=_internal earliest=-15m
  | head 1

dispatch:
  earliest_time: "-15m"
  latest_time: "now"
  max_count: 50000
  status_buckets: 0
  required_fields:
    - sourcetype

results:
  endpoint: auto
  output_mode: json
  count: 0
  offset: 0

safety:
  allow_old_earliest: false
  allow_index_wildcard: false

diagnostics:
  search_log: summary
  search_log_file: splunksearch.log
```

Secrets do not belong in YAML config. Continue to provide `SPLUNKBASEURL`,
`SPLUNKTOKEN`, `SPLUNKUSERNAME`, and `SPLUNKPASSWORD` through environment
variables or `.env`.

CLI flags override config values where both are set:

- `-app` overrides `app`
- `-o` overrides `output_file`
- `-earliest` and `-latest` add dispatch time bounds

Supported `results.endpoint` modes:

- `auto`: try the Splunk Search API v2 results endpoint, then fall back to v1
- `v2`: use `/services/search/v2/jobs/{sid}/results`
- `v1`: use `/services/search/jobs/{sid}/results/`

Use `results.count` and `results.offset` to request a specific result page.
The tool writes the response body returned by Splunk without merging multiple
pages.

Supported `mode` values:

- `job`: dispatch a search job, poll for completion, fetch `search.log`, then
  fetch results
- `export`: stream results directly from Splunk export; this does not create a
  search job ID and does not support `search.log` diagnostics

Supported `diagnostics.search_log` modes:

- `off`: do not fetch `search.log`
- `summary`: fetch and summarize execution duration, warnings, and errors
- `save`: fetch and save the full `search.log`
- `both`: summarize and save the full `search.log`

If `diagnostics.search_log_file` is omitted for `save` or `both`, the tool
derives a file name from the result output file, such as
`splunkresults.search.log`.

Example health-check configs are available in `examples/health/`. They include
read-only `_internal` and Splunk REST health searches with notes about required
permissions and Splunk Cloud caveats.

Run one with:

```bash
querysplunk -config examples/health/splunkd-health.yml
```

## Search job lifecycle and diagnostics

The tool dispatches searches as Splunk search jobs and polls the job until it
reaches a terminal state.

Successful terminal state:

- `DONE`

Failure terminal states are reported as errors, including:

- `FAILED`
- `CANCELLED`
- `INTERNAL_CANCEL`
- `USER_CANCEL`
- `BAD_INPUT_CANCEL`
- `QUIT`
- `PAUSE`
- `PAUSED`

While polling, the tool logs job state changes and includes available progress
fields such as `doneProgress`, `scanCount`, `eventCount`, and `resultCount`.

After a Splunk job ID exists, the tool can fetch the raw job log text from:

```text
/services/search/jobs/{sid}/search.log
```

The search log is analyzed for execution duration and warning/error lines.
Warnings and errors found in `search.log` are logged even if Splunk reports the
job state as `DONE`, because the job can complete with useful non-fatal
diagnostics. Large diagnostic output is bounded before being written to logs.

## Usage

Run `querysplunk -h` to see the supported flags. Logs are written to standard
error and result data is written to the output file selected by `-o` or
`output_file`.

### help

```
querysplunk -h
```

```text
Usage:
  querysplunk [options]

Run a Splunk search from a plain SPL file or from a structured YAML config.

Examples:
  querysplunk -q query.txt -o splunkresults.json
  querysplunk -q query.txt -earliest=-15m -latest=now
  querysplunk -config search.yml
  querysplunk -write-config search.yml
  querysplunk -write-config search.yml -force

Authentication and connection settings are read from environment variables:
  SPLUNKBASEURL
  SPLUNKTOKEN
  SPLUNKUSERNAME / SPLUNKPASSWORD
  SPLUNKTLSVERIFY
  SPLUNKTIMEOUT
  SPLUNKAPP

Use -e to load those values from .env in the working directory.

Safety controls block earliest values older than one year and explicit index=*
searches unless acknowledged with -allow-old-earliest, -allow-index-wildcard,
or YAML safety.allow_old_earliest / safety.allow_index_wildcard.

Options:
  -allow-index-wildcard
    	Allow searches that explicitly use index=*
  -allow-old-earliest
    	Allow earliest times older than the default one-year safety limit
  -app string
    	Override Splunk app context / namespace for the search
  -config string
    	Run a structured YAML search config
  -e	Load Splunk connection settings from .env
  -earliest string
    	Set dispatch earliest_time, such as -15m or 2026-07-10T00:00:00
  -force
    	Allow -write-config to overwrite an existing file
  -latest string
    	Set dispatch latest_time, such as now
  -o string
    	Write Splunk results to this file (default "splunkresults.json")
  -q string
    	Read the SPL search from this plain text file (default "query.txt")
  -write-config string
    	Write a starter YAML search config and exit
```

### integration tests

Build-tagged live tests exist for optional Splunk integration verification.
They require live Splunk credentials and are skipped when the required
environment variables are not set.

Run:

```bash
go test -v -tags integration ./...
(cd splunk && go test -v -tags integration ./...)
```

Required environment variables for integration runs:

- `SPLUNKBASEURL`
- either `SPLUNKTOKEN` or both `SPLUNKUSERNAME` and `SPLUNKPASSWORD`
- optional: `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, `SPLUNKAPP`

The root integration test runs the CLI with
`examples/health/splunkd-health.yml`. The `splunk` module integration test
exercises the lower-level Splunk client and uses `SPLUNK_INTEGRATION_QUERY`
when provided.

### GitHub Actions integration workflow

Repository CI runs unit tests and linting on `push` and `pull_request`.
Live Splunk integration tests are gated to manual runs only.

To run integration tests in GitHub Actions:

1. Go to `Actions` → `Go`
2. Click `Run workflow`
3. Enable **`run_integration_tests`**
4. Start the run

The workflow uses the GitHub environment named `AUTH_TOKEN`. Create these
environment secrets there for the integration step:

- `SPLUNKBASEURL`
- `SPLUNKTOKEN`
- `SPLUNKUSERNAME`

Optional environment secrets:

- `SPLUNKPASSWORD`
- `SPLUNKTLSVERIFY`
- `SPLUNKTIMEOUT`
- `SPLUNKAPP`
- `SPLUNK_INTEGRATION_QUERY`

`SPLUNK_INTEGRATION_QUERY` is optional. If it is not set, the integration test
uses the repository `query.txt` for the lower-level Splunk client test. The
CLI YAML integration test always runs `examples/health/splunkd-health.yml`.

For local integration runs, pass the same values through the normal environment
path instead of GitHub environment secrets.

## Release

Releases are built by GitHub Actions when a version tag is pushed. The release
workflow can also be run manually to dry-run packaging without creating a
GitHub Release.

Before tagging, merge the release branch to `main` and make sure the `Go`
workflow passes. To create a release:

```bash
git checkout main
git pull
git tag v1.1.0
git push origin v1.1.0
```

The `Release` workflow builds these assets and uploads them to the GitHub
release:

- `splunkquery-vX.Y.Z-darwin-amd64.tar.gz`
- `splunkquery-vX.Y.Z-darwin-arm64.tar.gz`
- `splunkquery-vX.Y.Z-linux-amd64.tar.gz`
- `splunkquery-vX.Y.Z-linux-arm64.tar.gz`
- `splunkquery-vX.Y.Z-windows-amd64.zip`
- `checksums.txt`

Each platform archive is a self-contained CLI bundle. It includes the
`splunkquery` binary, this README, `examples/health/`, and
`.agents/skills/querysplunk/` for local AI-assistant workflows. The `.agents`
content is an operating runbook for assistant tooling; it is not loaded by the
`querysplunk` binary.

To test release packaging in GitHub before tagging, run the `Release` workflow
manually and use a dry-run version such as `v0.0.0-dryrun`.

For local release-style packages, run:

```bash
make clean package VERSION=v1.1.0
make verify-package
```
