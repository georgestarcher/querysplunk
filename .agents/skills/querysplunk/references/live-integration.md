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

The root integration test runs `examples/health/splunkd-health.yml`. The splunk package integration test uses `SPLUNK_INTEGRATION_QUERY` when set, otherwise it falls back to `query.txt`.

In GitHub Actions, live Splunk integration is manual: run the `Go` workflow with `run_integration_tests=true`. The workflow expects secrets in the `AUTH_TOKEN` environment.
