# Defender detection examples

These examples provide bounded detection and threat-hunting searches for
authorized defenders. Validate each YAML file offline before execution and
review its data requirements, time range, and result sensitivity.

## AI command telemetry research

The [Splunk AI command telemetry mapping](AI-COMMAND-TELEMETRY.md) documents
observed `_audit`, `_internal`, extension-worker, and `search.log` fields for
Splunk AI Assistant activity. It includes a tested, bounded normalization SPL,
an Agent Threat Rules-oriented field map, sensitivity and retention guidance,
Splunk Cloud limitations, and the evidence boundary for future AI-agent
detections. The mapping defines what the starter pack can support without
inventing unavailable model, output, token, latency, or cross-source session
data.

## AI-agent starter pack

The experimental searches under `ai-agent/` identify:

- credential-bearing input that occurs before AI enrichment;
- `ai` followed by a separate downstream action-capable command;
- an exact `ai_result_N` field or `$ai_result_N$` substitution used by a later
  `map` or `script` command; and
- `ai` followed later by the separate Splunk `delete` command.

The `ai` command transforms search rows; it does not take those actions
independently. Read the [starter-pack guide](ai-agent/README.md) before
execution.

## Splunk Audit Logs detections

These searches use the
[CIM Splunk Audit Logs data model](https://help.splunk.com/en/splunk-cloud-platform/common-information-model/5.3/data-models/splunk-audit-logs).
The deployment must populate the corresponding datasets and make them visible
to the querysplunk account. Data-model acceleration is used when available;
the searches can also inspect unaccelerated data.

- `sensitive-search-activity.yml` identifies audited SPL containing credential
  store access, event deletion, data-writing commands, external actions,
  dynamic `map` execution, or other REST API access.
- `failed-search-activity.yml` summarizes searches whose audited `info` value
  is `failed`, preserving the SPL for investigation.

These are investigative leads rather than proof of malicious activity. REST,
`collect`, `outputlookup`, `map`, and failed searches all have legitimate uses.
Review the user, search, time range, and surrounding change context before
escalating.

Audit results can contain sensitive SPL, object names, and usernames. Keep
result files private and do not paste raw results into public issues or
unrestricted assistant conversations.

Validate without contacting Splunk:

```bash
querysplunk -validate-config examples/detections/sensitive-search-activity.yml
querysplunk -validate-config examples/detections/failed-search-activity.yml
```

Run one detection after reviewing its scope:

```bash
querysplunk -config examples/detections/sensitive-search-activity.yml
```

Both examples default to the last 24 hours and cap displayed detections at 200
rows. Adjust those bounds deliberately for the investigation.
