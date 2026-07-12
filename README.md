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

## Use as a Go package

Applications can load and safely prepare the same YAML used by the CLI:

```go
import "github.com/georgestarcher/querysplunk/v2/query"
```

Use `query.Load`, `query.LoadFile`, or `query.LoadFS` for strict YAML decoding.
`query.Prepare` applies caller overrides, defaults, typed safety analysis, and
conversion to `splunk.SearchOptions` in that order. The zero-value
`query.SafetyPolicy` blocks earliest values older than one calendar year and
explicit `index=*`. Prefer per-risk acknowledgements in YAML or
`query.Overrides`; `query.UnsafeAllowAll()` is an intentionally conspicuous
escape hatch and must not be used for untrusted searches.

`Prepared.Plan` returns the same credential-free effective configuration and
structured findings used by the CLI's offline `-validate-config` mode. Go
consumers can inspect that plan before choosing whether to execute it.

Prepared queries provide buffered `Search`, streaming `SearchTo`, and atomic
`SearchToFile` execution. Inspect warnings, violations, and acknowledgements
through `query.Finding`; `errors.Is` and `errors.As` work with
`query.ErrSafetyViolation` and `*query.ViolationError`. Credentials remain the
responsibility of `splunk.NewClient` and never belong in YAML.
Prepared job searches default to summarized, bounded `search.log` diagnostics;
set `diagnostics.search_log: off` explicitly to disable retrieval.

Bundled health files can be loaded with
`query.LoadFS(os.DirFS("."), "examples/health/splunkd-health.yml")` or embedded
in another application with `embed.FS`.

Applications can also import the lower-level REST client directly:

```go
import "github.com/georgestarcher/querysplunk/v2/splunk"
```

The package is part of this repository's v2 Go module and follows its release
versions. Use `splunk.NewClient` for application code. Transport, authentication,
job polling, and mutable request state remain package implementation details.

A typical application creates one client, authenticates early, reuses it for
concurrent searches, and closes it during shutdown:

```go
client, err := splunk.NewClient(splunk.Config{
    BaseURL: os.Getenv("SPLUNKBASEURL"),
    Token:   os.Getenv("SPLUNKTOKEN"),
    App:     "search",
    Logger:  slog.New(slog.NewJSONHandler(os.Stderr, nil)),
})
if err != nil {
    return err
}
defer client.Close()

ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
defer cancel()
if err := client.Authenticate(ctx); err != nil {
    return err
}

result, err := client.Search(ctx,
    "search index=_internal earliest=-15m | stats count",
    splunk.SearchOptions{
        DispatchParams: map[string][]string{"latest_time": {"now"}},
        ResultParams:   map[string][]string{"output_mode": {"json"}},
        SearchLog:      splunk.SearchLogModeSummary,
    },
)
if err != nil {
    var statusErr *splunk.HTTPStatusError
    var stateErr *splunk.JobStateError
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return err
    case errors.As(err, &statusErr):
        return fmt.Errorf("Splunk REST request failed with status %d: %w", statusErr.StatusCode, err)
    case errors.As(err, &stateErr):
        return fmt.Errorf("Splunk search ended in %s: %w", stateErr.State, err)
    default:
        return err
    }
}
```

`Client.Search` returns the unmodified Splunk response in `Result.Data`, plus
the job ID, final state, and bounded `search.log` diagnostics. Use
`Client.SearchTo` with an `io.Writer` to stream large job or export responses
without a temporary file or a complete in-memory copy. The client copies
parameter maps before use and is safe for concurrent searches after
authentication. Each returned byte slice belongs to the caller.

Existing search jobs can be resumed by SID with `InspectJob`, `WaitJob`,
`JobResults`, `JobResultsTo`, `JobResultsToFile`, `JobSearchLog`,
`JobSearchLogToFile`, and `CancelJob`. SIDs are validated before URL
construction. `WaitJob` never cancels a pre-existing remote job when its local
context ends; cancellation requires an explicit `CancelJob` call.

Set `splunk.Config.EventSink` to receive typed `RuntimeEvent` values for normal
and resumed jobs. Events are synchronous and serialized in increasing
`sequence` order for each client, including concurrent searches. A sink must
return quickly and must not call back into the same client. `EventSink` and
`Logger` are independent; normally configure one representation to avoid
duplicate lifecycle reporting.

Important package boundaries and limits:

- Package logging is disabled by default. Set `Config.Logger` to a
  concurrency-safe `*slog.Logger` to receive structured job progress,
  endpoint-fallback, duration, and bounded diagnostic-severity records. Use the
  logger's level filter to suppress informational progress while retaining
  warnings. Package logs exclude credentials, SPL, URLs, result data, complete
  search logs, and individual diagnostic lines.
- The low-level `splunk` package executes SPL without policy. Use the `query`
  package when consumers should share the CLI YAML schema and safety controls.
- TLS verification is on by default. `InsecureSkipVerify` is an explicit escape
  hatch for controlled development systems, not a production default.
- `Search` buffers result bodies in memory. Use `SearchTo` and a caller-owned
  writer for potentially large responses. A writer error can leave partial
  output; the returned `Result` still contains available job provenance.
- Export searches have no job ID or `search.log`.
- Treat result data and full search logs as sensitive. Do not log credentials,
  private URLs, SPL, or raw events. Retain only necessary provenance.
- Prefer synthetic `httptest` regression fixtures. Never commit real tokens,
  tenant URLs, private index names, or production events.

The package example is compiled by `go test`. Run focused and complete checks:

```bash
go test -race ./...
```

## Install a release

Release bundles are the easiest way to use querysplunk. They require no Go
toolchain, repository clone, administrator access, or knowledge of assistant
skill directories.

Choose the archive that matches your computer:

| Platform | Release asset |
| --- | --- |
| Apple Silicon Mac | `darwin-arm64.tar.gz` |
| Intel Mac | `darwin-amd64.tar.gz` |
| Linux x86-64 | `linux-amd64.tar.gz` |
| Linux ARM64 | `linux-arm64.tar.gz` |
| Windows x86-64 | `windows-amd64.zip` |

Download that archive and `checksums.txt` from the same GitHub Release. Verify
the archive before extracting it; [INSTALL.md](INSTALL.md) provides exact
commands for macOS, Linux, and Windows.

### Install it yourself

Extract the archive, open a terminal in the extracted directory, and run:

```bash
./install.sh
```

On Windows PowerShell:

```powershell
.\install.ps1
```

The installer places the command at `~/.local/bin/querysplunk` by default and
installs the bundled skill for locally detected Codex and Claude Code
installations. It prints PATH guidance when needed. It does not read, prompt
for, copy, or configure Splunk credentials.

Verify the result:

```bash
querysplunk -version
```

Use `--agent codex|claude|both|none` or PowerShell `-Agent` to select assistant
targets explicitly. Use `./install.sh --help` for custom locations and other
options.

### Ask Codex or Claude Code to install it

Open the assistant in the extracted release directory and give it this prompt:

> Read INSTALL.md and .agents/skills/querysplunk/SKILL.md in this extracted
> release. Install querysplunk for my available local assistants using the
> bundled installer. Do not configure or display Splunk credentials. Verify the
> installed command and skill files when finished.

The agent should run the bundled installer rather than improvise file-copy or
PATH operations.

### Use it from an assistant

Start a new assistant session if this is the first skill installed for that
assistant. Invoke it directly with `$querysplunk` in Codex or `/querysplunk` in
Claude Code, or ask naturally:

> Use querysplunk to create a time-bounded saved YAML search for the last 30
> minutes. Generate the skeleton, edit it, and validate it offline. Show me the
> effective plan and safety findings, but do not contact Splunk until I approve.

After approval, ask the agent to execute the YAML with machine-readable runtime
events and summarize the result and bounded diagnostics. The installed skill
instructs agents to keep credentials out of YAML and chat, preserve safety
controls, separate events from results, resume interrupted jobs by SID, and
require explicit authorization before cancellation.

The bundled skill also includes deterministic preflight and recovery rules, SPL
authoring guidance, bounded result analysis, health-diagnostic interpretation,
and a non-sensitive session handoff template. These playbooks tell an agent when
to retry, when to resume by SID, and when to stop for user correction instead of
blindly redispatching searches.

Splunk connection values remain environment configuration. See
[Configuration environment](#configuration-environment) before the first live
search.

### Upgrade yourself or through an agent

To upgrade from an extracted newer release, run `./install.sh --upgrade` or
`.\install.ps1 -Upgrade`. Upgrades preserve saved YAML, results, environment
files, credentials, configuration, and unrelated skills; downgrades require an
explicit acknowledgement.

Or open the assistant in the newer extracted bundle and say:

> Read INSTALL.md and .agents/skills/querysplunk/SKILL.md in this extracted
> release. Upgrade my existing querysplunk installation using the bundled
> installer. Preserve my YAML files, credentials, configuration, results, and
> unrelated skills. Do not downgrade unless I explicitly approve it. Verify
> the installed version and assistant skills when finished.

See [INSTALL.md](INSTALL.md) for checksum commands, custom destinations, PATH
guidance, complete upgrade behavior, first use, and removal.

## Build from source

```bash
brew install go
# or upgrade if already installed
brew upgrade go
```

Build the CLI locally:

```bash
go build -o querysplunk .
```

## Public issue submissions

This is a public repository. Use the GitHub issue templates for bug reports,
feature requests, and proposed YAML saved searches. Before submitting an issue,
remove Splunk credentials, private URLs, tenant names, private index names,
sensitive SPL, customer data, and deployment-specific details.

Proposed reusable YAML searches should use the saved-search review template and
include time bounds, expected access requirements, and Splunk deployment impact.

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

### Validate a YAML config offline

Validate schema, defaults, explicit CLI overrides, and safety policy without
credentials or a Splunk connection:

```bash
querysplunk -validate-config search.yml
```

The command writes a deterministic YAML execution plan to standard output. The
plan contains `valid`, the effective credential-free `config`, and structured
`findings`. Blocking safety findings produce `valid: false` and a nonzero exit;
warnings and explicitly acknowledged risks remain successful. The normal
`-app`, `-o`, `-earliest`, `-latest`, and safety acknowledgement flags are
applied before validation.

Offline validation never loads `.env`, reads Splunk credentials, or contacts
Splunk. If YAML omits `app`, an already-exported `SPLUNKAPP` is included so the
plan matches normal execution; `-app` takes precedence. A successful plan
verifies querysplunk configuration and safety only; it does not prove Splunk
authorization, app visibility, SPL semantics, or live execution.

Exit status is `0` for a valid plan, `1` for invalid configuration or a blocked
search, and `2` for conflicting CLI modes. Validation errors are written to
standard error so standard output remains parseable YAML.

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

### Resume an existing search job

Use a Splunk search job ID (SID) to reconnect without reading SPL or dispatching
a new search:

```bash
querysplunk -job-sid 1258421375.19 -job-action status
querysplunk -job-sid 1258421375.19 -job-action wait
querysplunk -job-sid 1258421375.19 -job-action results -o results.json
querysplunk -job-sid 1258421375.19 -job-action search-log
querysplunk -job-sid 1258421375.19 -job-action search-log -o search.log
querysplunk -job-sid 1258421375.19 -job-action cancel
```

`status`, `wait`, and `cancel` emit JSON summaries. `results` atomically writes
the unmodified results response to `-o`, which defaults to
`splunkresults.json`, and emits a JSON file summary. `search-log` writes the raw
log to standard output; with an explicit `-o`, it atomically saves the raw log
and emits a bounded JSON diagnostic summary.

Job-action usage errors exit `2`; authentication, context, REST, terminal-state,
and local I/O failures exit `1`. Errors are written to standard error so JSON
or raw data on standard output remains usable. An interrupted `wait` does not
cancel the remote job. Cancellation is always explicit and completed jobs are
handled idempotently without posting a control action.

Possession of a SID does not bypass Splunk authorization, app namespace, job
ownership, sharing, or retention. A job may be unavailable after its TTL or to
a different user or app context. Splunk documents job status, results, control,
and `search.log` under its [search job REST endpoints](https://help.splunk.com/en/splunk-enterprise/leverage-rest-apis/rest-api-reference/10.4/search-endpoints/search-endpoint-descriptions).

### Machine-readable runtime events

Add `-json-events` to a live search or resumed-job command to write one compact
JSON object per line to standard error:

```bash
querysplunk -json-events -config search.yml 2>events.jsonl
querysplunk -json-events -job-sid 1258421375.19 -job-action wait 2>events.jsonl
```

Standard output remains reserved for YAML plans, JSON operation summaries, raw
results, or raw `search.log`. Without `-json-events`, the CLI retains its
human-readable lifecycle logging. JSON events contain these stable fields when
applicable:

- `sequence`, `time`, `kind`, and `severity`
- `operation`, `sid`, `state`, and progress/count fields
- `from_endpoint` and `to_endpoint` for fallback
- `execution_duration`, `warning_count`, and `error_count`
- `output_file`, `cancel_requested`, and `outcome`

Event kinds are `job_dispatched`, `job_status`, `endpoint_fallback`,
`diagnostics`, `cancellation`, `output_saved`, and `operation`. Events never
contain credentials, private base URLs, SPL, results, raw search logs, or
individual diagnostic lines. Delivery is synchronous and ordering is defined
by the per-client `sequence`; consumers should not assume wall-clock spacing.

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

Run a Splunk search or reconnect to an existing Splunk search job.

Examples:
  querysplunk -version
  querysplunk -validate-config search.yml
  querysplunk -json-events -config search.yml
  querysplunk -job-sid 1258421375.19 -job-action status
  querysplunk -job-sid 1258421375.19 -job-action wait
  querysplunk -job-sid 1258421375.19 -job-action results -o results.json
  querysplunk -job-sid 1258421375.19 -job-action search-log
  querysplunk -job-sid 1258421375.19 -job-action cancel
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
  -job-action string
	Act on -job-sid: status, wait, results, search-log, or cancel
  -job-sid string
	Use an existing Splunk search job ID
  -json-events
	Write machine-readable lifecycle events as JSON Lines to stderr
  -o string
    	Write Splunk results to this file (default "splunkresults.json")
  -q string
    	Read the SPL search from this plain text file (default "query.txt")
  -validate-config string
	Validate a YAML search config offline and print its effective plan
  -version
	Print version and build metadata, then exit
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
```

Required environment variables for integration runs:

- `SPLUNKBASEURL`
- either `SPLUNKTOKEN` or both `SPLUNKUSERNAME` and `SPLUNKPASSWORD`
- optional: `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, `SPLUNKAPP`

The root integration test runs the CLI with
`examples/health/splunkd-health.yml`. The `splunk` package integration test
exercises the lower-level Splunk client and uses `SPLUNK_INTEGRATION_QUERY`
when provided.

### GitHub Actions integration workflow

Repository CI builds and runs unit tests on Linux, macOS, and Windows for every
`push` and `pull_request`. Race tests run on Linux and macOS; Windows runs build
and unit coverage because race detection is not part of the repository's
reliable Windows runner contract. Linux also runs formatting, coverage, fuzzing,
vet, lint, module-tidy, and vulnerability checks. Live Splunk integration tests
are gated to manual runs only and depend on both CI layers passing.

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
git tag v2.1.0
git push origin v2.1.0
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
`splunkquery` binary, the platform installer, `INSTALL.md`, this README,
`examples/health/`, and
`.agents/skills/querysplunk/` for local AI-assistant workflows. The `.agents`
content is a portable Agent Skill for Codex and Claude Code; it is not loaded by
the `querysplunk` binary until installed into an assistant's skill directory.

Identify an installed binary without credentials or a Splunk connection:

```bash
querysplunk -version
```

Release binaries report a stable line containing the release version and source
commit. Local builds that do not inject release metadata report `dev` and
`unknown`.

To test release packaging in GitHub before tagging, run the `Release` workflow
manually and use a dry-run version such as `v0.0.0-dryrun`.

For local release-style packages, run:

```bash
make clean package VERSION=v2.1.0
make verify-package VERSION=v2.1.0
```
