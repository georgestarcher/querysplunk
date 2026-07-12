# Installation and upgrade reference

Release bundles contain a platform binary, an installer, `INSTALL.md`, examples,
and this complete skill directory. Installation never requires Go and never
handles Splunk credentials.

For macOS or Linux, run `./install.sh`. For Windows, run `./install.ps1`.
Assistant targets can be selected as `auto`, `codex`, `claude`, `both`, or
`none`. The command is installed as `querysplunk`, and the skill is copied to
the selected assistants' personal skill directories.

For an upgrade, run `./install.sh --upgrade` or
`.\install.ps1 -Upgrade`. The installer replaces only querysplunk-owned binary
and skill files, verifies the resulting version, preserves user files and
unrelated skills, and blocks downgrades without explicit acknowledgement.

When asked to install or upgrade from an extracted bundle, read the top-level
`INSTALL.md`, run its deterministic installer, and verify the command plus
installed skill files. Do not recreate installer operations manually, modify
shell configuration without approval, or inspect credentials.
