$ErrorActionPreference = "Stop"

$RepoDir = Split-Path -Parent $PSScriptRoot
$WorkDir = Join-Path ([IO.Path]::GetTempPath()) ("querysplunk-installer-" + [guid]::NewGuid().ToString("N"))

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function New-Bundle([string]$Destination, [string]$Version) {
    New-Item -ItemType Directory -Path (Join-Path $Destination ".agents/skills") -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $RepoDir "install.ps1") -Destination (Join-Path $Destination "install.ps1")
    Copy-Item -LiteralPath (Join-Path $RepoDir ".agents/skills/querysplunk") -Destination (Join-Path $Destination ".agents/skills/querysplunk") -Recurse
    Push-Location $RepoDir
    try {
        & go build -trimpath -ldflags "-X main.version=$Version -X main.commit=installer-test" -o (Join-Path $Destination "splunkquery.exe") .
        if ($LASTEXITCODE -ne 0) { throw "failed to build installer test binary" }
    } finally {
        Pop-Location
    }
}

try {
    $HomeDir = Join-Path $WorkDir "home with spaces"
    $BinDir = Join-Path $WorkDir "bin with spaces"
    $BundleV1 = Join-Path $WorkDir "bundle-v1"
    $BundleV2 = Join-Path $WorkDir "bundle-v2"
    New-Item -ItemType Directory -Path (Join-Path $HomeDir ".codex/skills/other"), (Join-Path $HomeDir ".claude"), (Join-Path $HomeDir "saved") -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $HomeDir ".codex/skills/other/KEEP") -Value "keep"
    Set-Content -LiteralPath (Join-Path $HomeDir "saved/user.yml") -Value "search: keep"
    Set-Content -LiteralPath (Join-Path $HomeDir "saved/.env.test") -Value "SPLUNKTOKEN=placeholder-not-a-secret"
    New-Bundle $BundleV1 "v1.0.0"
    New-Bundle $BundleV2 "v2.0.0"

    & (Join-Path $BundleV1 "install.ps1") -Agent both -HomeDir $HomeDir -BinDir $BinDir | Out-Null
    $TargetBinary = Join-Path $BinDir "querysplunk.exe"
    Assert-True ((& $TargetBinary -version) -eq "querysplunk version=v1.0.0 commit=installer-test") "fresh binary installation failed"
    Assert-True (Test-Path -LiteralPath (Join-Path $HomeDir ".codex/skills/querysplunk/SKILL.md")) "Codex skill was not installed"
    Assert-True (Test-Path -LiteralPath (Join-Path $HomeDir ".claude/skills/querysplunk/SKILL.md")) "Claude skill was not installed"

    & (Join-Path $BundleV1 "install.ps1") -Agent both -HomeDir $HomeDir -BinDir $BinDir | Out-Null
    $blocked = $false
    try { & (Join-Path $BundleV2 "install.ps1") -Agent both -HomeDir $HomeDir -BinDir $BinDir | Out-Null } catch { $blocked = $true }
    Assert-True $blocked "newer version installed without -Upgrade"
    & (Join-Path $BundleV2 "install.ps1") -Upgrade -Agent both -HomeDir $HomeDir -BinDir $BinDir | Out-Null
    Assert-True ((& $TargetBinary -version) -eq "querysplunk version=v2.0.0 commit=installer-test") "upgrade failed"
    Assert-True (Test-Path -LiteralPath (Join-Path $HomeDir ".codex/skills/other/KEEP")) "upgrade removed an unrelated skill"
    Assert-True (Test-Path -LiteralPath (Join-Path $HomeDir "saved/user.yml")) "upgrade removed user YAML"
    Assert-True (Test-Path -LiteralPath (Join-Path $HomeDir "saved/.env.test")) "upgrade removed user configuration"

    $blocked = $false
    try { & (Join-Path $BundleV1 "install.ps1") -Upgrade -Agent both -HomeDir $HomeDir -BinDir $BinDir | Out-Null } catch { $blocked = $true }
    Assert-True $blocked "downgrade was not blocked"
    & (Join-Path $BundleV1 "install.ps1") -Upgrade -AllowDowngrade -Agent none -HomeDir $HomeDir -BinDir $BinDir | Out-Null
    Assert-True ((& $TargetBinary -version) -eq "querysplunk version=v1.0.0 commit=installer-test") "authorized downgrade failed"

    Write-Output "PowerShell installer tests passed"
} finally {
    Remove-Item -LiteralPath $WorkDir -Recurse -Force -ErrorAction SilentlyContinue
}
