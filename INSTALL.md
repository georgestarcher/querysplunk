# Install and upgrade querysplunk

Release archives are self-contained. They do not require Go, a repository
clone, administrator access, or knowledge of assistant skill directories.

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

Start a new assistant session after creating an assistant's top-level skills
directory for the first time. Invoke the installed skill as `$querysplunk` in
Codex or `/querysplunk` in Claude Code, or ask naturally for help preparing or
running a querysplunk search.

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

## Remove

Remove the installed command and only the assistant skill directories you chose:

```bash
rm "$HOME/.local/bin/querysplunk"
rm -rf "$HOME/.codex/skills/querysplunk"
rm -rf "$HOME/.claude/skills/querysplunk"
```

On Windows, remove `querysplunk.exe` from the selected `-BinDir` and remove the
corresponding `querysplunk` folders under `.codex\skills` and `.claude\skills`.
