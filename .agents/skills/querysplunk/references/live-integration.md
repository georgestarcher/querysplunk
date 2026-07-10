# Live integration reference for assistants

Local live checks require:

- `SPLUNKBASEURL`
- either `SPLUNKTOKEN` or both `SPLUNKUSERNAME` and `SPLUNKPASSWORD`
- optional `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, `SPLUNKAPP`

Run root CLI YAML integration:

```bash
go test -v -tags integration ./...
```

Run lower-level Splunk client integration:

```bash
(cd splunk && go test -v -tags integration ./...)
```

The root integration test runs `examples/health/splunkd-health.yml`. The splunk module integration test uses `SPLUNK_INTEGRATION_QUERY` when set, otherwise it falls back to `query.txt`.

In GitHub Actions, live Splunk integration is manual: run the `Go` workflow with `run_integration_tests=true`. The workflow expects secrets in the `AUTH_TOKEN` environment.
