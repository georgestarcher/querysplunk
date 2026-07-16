# Release bundle reference for assistants

Release archives are intended to be self-contained CLI bundles. Each archive should include:

- the platform binary
- `install.sh` on macOS/Linux or `install.ps1` on Windows
- `INSTALL.md`
- `README.md`
- `RELEASE_NOTES.md`
- `THIRD_PARTY_NOTICES.md`
- `examples/health/`, `examples/rest/`, `examples/detections/`, and
  `examples/pentest/`
- `.agents/skills/querysplunk/`

The GitHub release should publish the same curated `RELEASE_NOTES.md`, all five
platform archives, and `checksums.txt`. Do not substitute generic generated
notes when curated notes are present.

The installer exposes the bundled binary as the consistent `querysplunk`
command and can install or upgrade the portable skill for Codex, Claude Code,
or both without reading credentials.

Local packaging:

```bash
make clean package VERSION=vX.Y.Z
```

Verify package contents:

```bash
scripts/verify-package-contents.sh dist
```

Do not include local `.env` files, generated result JSON, build cache files, or machine-specific paths in release archives.
