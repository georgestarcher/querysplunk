# querysplunk

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

## Configuration environment

You may use a `.env` file with the `-e` flag. Otherwise, the tool reads these
values from operating system environment variables.

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
settings beyond the SPL text, such as app context, output file, dispatch
parameters, result parameters, or search log diagnostics.

```bash
./splunkquery-darwin -config search.yml
```

For quick one-off bounds without YAML, use dispatch-level time flags:

```bash
./splunkquery-darwin -q query.txt -earliest=-15m -latest=now
```

If neither the SPL nor dispatch parameters include `earliest` / `latest` time
bounds, the tool logs a warning. Existing unbounded searches still run, but
Splunk REST searches can otherwise run over all time.

To generate a starter config:

```bash
./splunkquery-darwin -write-config search.yml
```

The command refuses to overwrite an existing file unless `-force` is also set:

```bash
./splunkquery-darwin -write-config search.yml -force
```

Example:

```yaml
app: search
output_file: splunkresults.json
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
  output_mode: json
  count: 0
  offset: 0

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

Supported `diagnostics.search_log` modes:

- `off`: do not fetch `search.log`
- `summary`: fetch and summarize execution duration, warnings, and errors
- `save`: fetch and save the full `search.log`
- `both`: summarize and save the full `search.log`

If `diagnostics.search_log_file` is omitted for `save` or `both`, the tool
derives a file name from the result output file, such as
`splunkresults.search.log`.

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

Place the `.env` file next to the executable when using `-e`.

### help

```
./splunkquery-darwin -h
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

Use -e to load those values from a .env file in the working directory.

Options:
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

Run:

```bash
cd splunk
go test -v -tags integration ./...
```

Required environment variables for integration runs:

- `SPLUNKBASEURL`
- either `SPLUNKTOKEN` or both `SPLUNKUSERNAME` and `SPLUNKPASSWORD`
- optional: `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, `SPLUNKAPP`

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
uses the repository `query.txt`.

You can also pass integration values through the normal environment path as
an alternative to repository secrets.

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

To test release packaging in GitHub before tagging, run the `Release` workflow
manually and use a dry-run version such as `v0.0.0-dryrun`.

For local release-style packages, run:

```bash
make clean package VERSION=v1.1.0
```
