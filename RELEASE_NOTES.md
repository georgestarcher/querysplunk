# querysplunk v2.3.0

Released July 16, 2026.

v2.3.0 expands querysplunk from a safe search runner into a versioned,
assistant-ready library of reusable Splunk diagnostics and security searches.
The CLI and public Go module remain backward compatible with v2.2.0.

## Versioned YAML search library

- Bundled searches now use `schema_version: "1"` and include descriptive
  metadata, platform and capability requirements, provenance, interpretation,
  and review status.
- Existing runtime-only YAML remains valid and is interpreted as schema
  version 1.
- Provenance records distinguish original querysplunk searches from manually
  adapted third-party concepts.

## Enforced result safety

- `result_handling` describes sensitivity, credential content, assistant display
  limits, recommended file permissions, and retention expectations.
- `result_contract` validates required fields, empty-result policy, and maximum
  row counts before results are presented to an assistant or caller.
- The CLI and public `query` package enforce the same contracts and preserve
  atomic output behavior.

## Expanded bundled searches

- Scheduled-search workflows can diagnose failed jobs with retained
  `search.log` data and profile successful jobs that approach or exceed their
  observed schedule interval.
- Health searches now cover system messages, orphaned scheduled searches,
  scheduler activity, Splunk Audit web-service errors, and failed modular
  actions.
- Read-only REST examples inspect saved-search definitions, macros, lookup
  definitions, and bounded lookup previews.
- Defender detections cover sensitive or failed audited search activity.
- Authorized penetration-testing examples cover Splunk's stored-credential
  endpoint and possible passwords pasted into username fields. Their results
  are explicitly classified as secret and prohibited from raw assistant
  display.

## AI-agent detection starter pack

- A tested Splunk AI command telemetry map documents observable evidence and
  its limits.
- Four experimental searches identify sensitive input before AI enrichment,
  downstream action-capable commands, exact AI-result references used by
  dynamic execution, and AI enrichment followed by Splunk `delete`.
- Matching safeguards distinguish quoted examples from executable commands,
  inspect all relevant post-AI dynamic commands, and require exact
  `ai_result_N` fields or `$ai_result_N$` substitutions.
- The starter pack credits the Agent Threat Rules project and pins the exact
  upstream revision used for inspiration.

## Documentation and project security

- The README, installer guide, assistant skill, examples, and project wiki now
  provide aligned first-use and safety guidance.
- The security policy documents private vulnerability reporting and supported
  release expectations.
- The Splunk MCP comparison explains when MCP network access or interactive
  discovery complements querysplunk's repeatable YAML, CI, SID recovery, and
  bounded diagnostics.

## Upgrade

Download the archive and `checksums.txt` from the
[v2.3.0 release](https://github.com/georgestarcher/querysplunk/releases/tag/v2.3.0),
verify the checksum, extract it, and run:

```bash
./install.sh --upgrade
```

On Windows PowerShell, run `./install.ps1 -Upgrade`. The installer preserves
saved YAML, results, credentials, environment files, shell settings, and
unrelated assistant skills.

See the [full comparison](https://github.com/georgestarcher/querysplunk/compare/v2.2.0...v2.3.0)
for every merged change.
