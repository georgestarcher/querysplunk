# Security Policy

## Supported versions

Security fixes are provided for the latest published querysplunk release. If a
report affects an older version, verify it against the latest release before
submitting it when it is safe to do so.

## Report a vulnerability

Use GitHub's **Report a vulnerability** option on the repository Security page
to submit a private report. Do not open a public issue for a suspected
vulnerability.

Include enough information to reproduce and assess the issue:

- affected querysplunk version and operating system;
- affected command, Go package, installer, workflow, or release artifact;
- expected and observed behavior;
- minimal reproduction steps or a proof of concept; and
- the potential impact and any suggested mitigation.

Do not include live credentials, authorization headers, private Splunk URLs,
production events, complete search logs, or other sensitive data. Use redacted
or synthetic examples instead.

You should receive an acknowledgement within five business days. Validation,
remediation, and disclosure timelines depend on severity and complexity. Please
allow time for a fix and coordinated release before publishing details.

## Scope

Reports are especially useful when they concern credential handling, TLS or
authentication behavior, unsafe URL or SID construction, unintended file
writes, safety-policy bypasses, installer behavior, release integrity, or
GitHub Actions security. General support questions and non-security defects
belong in the public issue tracker.
