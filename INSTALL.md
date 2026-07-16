# Install and upgrade querysplunk

Release archives are self-contained. They do not require Go, a repository
clone, administrator access, or knowledge of assistant skill directories.
Read the bundled `RELEASE_NOTES.md` before an upgrade to understand new search
families, result-handling requirements, and any operator action.

Choose the archive for your computer:

| Platform | Release asset |
| --- | --- |
| Apple Silicon Mac | `splunkquery-vX.Y.Z-darwin-arm64.tar.gz` |
| Intel Mac | `splunkquery-vX.Y.Z-darwin-amd64.tar.gz` |
| Linux x86-64 | `splunkquery-vX.Y.Z-linux-amd64.tar.gz` |
| Linux ARM64 | `splunkquery-vX.Y.Z-linux-arm64.tar.gz` |
| Windows x86-64 | `splunkquery-vX.Y.Z-windows-amd64.zip` |

## Verify the download

Download the archive for your platform and `checksums.txt` from the same GitHub
Release. Verify the selected archive before extracting it. For example, on
macOS:

```bash
grep 'darwin-arm64.tar.gz' checksums.txt | shasum -a 256 -c -
```

On Linux, use `sha256sum -c` with the matching checksum line. On Windows, use
`Get-FileHash -Algorithm SHA256` and compare it with `checksums.txt`.

## Install

Extract the archive, open a terminal in the extracted directory, and run:

```bash
./install.sh
```

On Windows PowerShell:

```powershell
.\install.ps1
```

The default installation is user-local. It installs the command as
`querysplunk` under `~/.local/bin` and automatically installs the bundled skill
for locally detected Codex and Claude Code installations. It never reads or
configures Splunk credentials.

Recommended setup order:

1. Verify the archive checksum.
2. Run the bundled installer.
3. Verify the installed command with `querysplunk -version`.
4. Generate or validate YAML offline with `querysplunk -validate-config`.
5. Export Splunk credentials only when you are ready to run a live search.

The installer changes only:

- the selected binary destination's `querysplunk` executable;
- `~/.codex/skills/querysplunk/` when Codex is selected;
- `~/.claude/skills/querysplunk/` when Claude Code is selected.

It does not edit shell startup files, saved searches, results, environment
files, credentials, or unrelated assistant skills.

Select assistant targets explicitly when needed:

```bash
./install.sh --agent codex
./install.sh --agent claude
./install.sh --agent both
./install.sh --agent none
```

PowerShell uses `-Agent codex`, `-Agent claude`, `-Agent both`, or
`-Agent none`. Use `--bin-dir` / `-BinDir` for a custom command directory. The
installer prints exact PATH guidance when that directory is not active.

Use `--home-dir DIR` on macOS/Linux or `-HomeDir DIR` on Windows to install
assistant skills under a different home directory. This changes the skill
targets only, such as `DIR/.codex/skills/querysplunk`; it does not move the
binary, configure credentials, or edit the selected profile. Most users should
keep the default. The option is useful for alternate local profiles, isolated
automation, and installation testing. Use `--bin-dir` / `-BinDir` separately
when the executable itself should be installed elsewhere.

Start a new assistant session after creating an assistant's top-level skills
directory for the first time. Invoke the installed skill as `$querysplunk` in
Codex or `/querysplunk` in Claude Code, or ask naturally for help preparing or
running a querysplunk search. The skill includes focused playbooks for
preflight and recovery, SPL authoring, bounded result analysis, health
diagnostics, and non-sensitive session handoff.
The installed skill also understands the bundled schema metadata, result
contracts, scheduled-search log analysis, read-only REST inspection, and
AI-agent detection safeguards.

Recommended first request:

> Use querysplunk to create a time-bounded saved YAML search for the last 30
> minutes. Generate the skeleton and validate it offline. Show me the effective
> plan and safety findings, but do not execute it until I approve.

## Ask an agent to install it

Open Codex or Claude Code in the extracted release directory and say:

> Read INSTALL.md and .agents/skills/querysplunk/SKILL.md in this extracted
> release. Install querysplunk for my available local assistants using the
> bundled installer. Do not configure or display Splunk credentials. Verify the
> installed command and skill files when finished.

The agent should run the bundled installer rather than reproduce its file
operations manually.

## Upgrade

Download and verify the newer release, extract it, and run:

```bash
./install.sh --upgrade
```

On Windows:

```powershell
.\install.ps1 -Upgrade
```

An upgrade replaces only the querysplunk command and querysplunk-owned skill
directories. It preserves saved YAML, result files, environment files,
credentials, shell configuration, and unrelated skills. Reinstalling the same
version verifies or repairs those managed files. Downgrades are blocked unless
you explicitly add `--allow-downgrade` or `-AllowDowngrade`.

To ask an agent to upgrade it:

> Read INSTALL.md and .agents/skills/querysplunk/SKILL.md in this extracted
> release. Upgrade my existing querysplunk installation using the bundled
> installer. Preserve my YAML files, credentials, configuration, results, and
> unrelated skills. Do not downgrade unless I explicitly approve it. Verify
> the installed version and assistant skills when finished.

## First use

Verify the installed release and generate a saved-search skeleton:

```bash
querysplunk -version
querysplunk -write-config search.yml
querysplunk -validate-config search.yml
```

Validation is offline and does not require credentials. After reviewing the
effective plan and safety findings, configure Splunk connection settings in
the environment and run the approved search. Use `-json-events` for agent-safe
runtime progress and `-job-sid` to resume an existing job.

For AI-assisted use, keep the README as the short user-facing entry point and
this file as the detailed install reference. Ask the assistant to validate YAML
before execution and to summarize outputs without printing credentials, private
URLs, raw result files, or complete search logs. The bundled querysplunk skill
contains the exact preflight, execution, resume, and cancellation rules agents
should follow.

For a live search, the assistant should follow this order:

1. Generate YAML with `querysplunk -write-config`.
2. Add bounded SPL and the intended app, output, and diagnostic settings.
3. Validate with `querysplunk -validate-config`.
4. Present the effective plan and any safety findings.
5. Wait for approval before contacting Splunk.
6. Execute with `-json-events`, keeping events separate from result output.
7. Summarize result counts and bounded diagnostics without exposing secrets or
   dumping raw sensitive output into chat.
8. Preserve the SID so another session can inspect, wait for, or retrieve the
   job. Cancellation always requires explicit authorization.

## Remove

Remove the installed command and only the assistant skill directories you chose:

```bash
rm "$HOME/.local/bin/querysplunk"
rm -rf "$HOME/.codex/skills/querysplunk"
rm -rf "$HOME/.claude/skills/querysplunk"
```

On Windows, remove `querysplunk.exe` from the selected `-BinDir` and remove the
corresponding `querysplunk` folders under `.codex\skills` and `.claude\skills`.
