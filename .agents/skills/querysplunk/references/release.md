# Release bundle reference for assistants

Release archives are intended to be self-contained CLI bundles. Each archive should include:

- the platform binary
- `README.md`
- `examples/health/`
- `.agents/skills/querysplunk/`

Local packaging:

```bash
make clean package VERSION=vX.Y.Z
```

Verify package contents:

```bash
scripts/verify-package-contents.sh dist
```

Do not include local `.env` files, generated result JSON, build cache files, or machine-specific paths in release archives.
