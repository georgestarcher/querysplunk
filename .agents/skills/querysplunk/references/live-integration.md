# Live integration reference for assistants

Local live checks require:

- `SPLUNKBASEURL`
- either `SPLUNKTOKEN` or both `SPLUNKUSERNAME` and `SPLUNKPASSWORD`
- optional `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, `SPLUNKAPP`

Run root CLI YAML integration:

```bash
go test -v -tags integration ./...
```

Run only the lower-level Splunk client integration:

```bash
go test -v -tags integration ./splunk
```

The root live test exercises YAML through the public `query` package. Synthetic
tests under `./query` cover strict loading, safety findings, streaming,
cancellation, diagnostics, and atomic output replacement.

The root integration test runs `examples/health/splunkd-health.yml`. The splunk package integration test uses `SPLUNK_INTEGRATION_QUERY` when set, otherwise it falls back to `query.txt`.

The lower-level integration test reconnects to the bounded search's SID,
inspects and waits on it, fetches results and `search.log`, and verifies that
cancelling an already-completed job is idempotent. Active cancellation remains
covered synthetically so live validation never cancels an unrelated job.

The live package test also captures typed runtime events and verifies required
event kinds without printing event payloads, SIDs, credentials, or URLs.

In GitHub Actions, live Splunk integration is manual: run the `Go` workflow with `run_integration_tests=true`. The workflow expects secrets in the `AUTH_TOKEN` environment.
