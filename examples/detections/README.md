# Defender detection examples

These examples provide bounded detection and threat-hunting searches for
authorized defenders. Validate each YAML file offline before execution and
review its data requirements, time range, and result sensitivity.

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
