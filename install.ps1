[CmdletBinding()]
param(
    [ValidateSet("auto", "codex", "claude", "both", "none")]
    [string]$Agent = "auto",
    [string]$HomeDir = $HOME,
    [string]$BinDir = "",
    [switch]$Upgrade,
    [switch]$AllowDowngrade
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($HomeDir)) {
    throw "HOME is not set; use -HomeDir"
}
if ([string]::IsNullOrWhiteSpace($BinDir)) {
    $BinDir = Join-Path $HomeDir ".local/bin"
}
if ($AllowDowngrade -and -not $Upgrade) {
    throw "-AllowDowngrade requires -Upgrade"
}

$SourceBinary = Join-Path $PSScriptRoot "splunkquery.exe"
$SkillSource = Join-Path $PSScriptRoot ".agents/skills/querysplunk"
if (-not (Test-Path -LiteralPath $SourceBinary -PathType Leaf)) {
    throw "release bundle is missing splunkquery.exe"
}
$SkillFile = Join-Path $SkillSource "SKILL.md"
if (-not (Test-Path -LiteralPath $SkillFile -PathType Leaf)) {
    throw "release bundle is missing the querysplunk skill"
}
$SkillText = Get-Content -LiteralPath $SkillFile -Raw
if ($SkillText -notmatch '(?m)^name:\s+querysplunk\s*$' -or $SkillText -notmatch '(?m)^description:\s+\S.+$') {
    throw "querysplunk skill has invalid frontmatter"
}

function Get-BinaryVersion([string]$Path) {
    $output = (& $Path -version 2>$null) -join " "
    if ($LASTEXITCODE -ne 0 -or $output -notmatch '^querysplunk version=([^\s]+) commit=[^\s]+$') {
        throw "could not read binary version"
    }
    return $Matches[1]
}

function Get-SemVerParts([string]$Version) {
    if ($Version -notmatch '^v?(\d+)\.(\d+)\.(\d+)(?:-([^+]+))?(?:\+.*)?$') {
        return $null
    }
    return [pscustomobject]@{
        Core = @([int64]$Matches[1], [int64]$Matches[2], [int64]$Matches[3])
        Pre = if ($Matches[4]) { $Matches[4].Split('.') } else { @() }
    }
}

function Compare-SemVer([string]$Left, [string]$Right) {
    $leftParts = Get-SemVerParts $Left
    $rightParts = Get-SemVerParts $Right
    if ($null -eq $leftParts -or $null -eq $rightParts) {
        return $null
    }
    for ($i = 0; $i -lt 3; $i++) {
        if ($leftParts.Core[$i] -lt $rightParts.Core[$i]) { return -1 }
        if ($leftParts.Core[$i] -gt $rightParts.Core[$i]) { return 1 }
    }
    if ($leftParts.Pre.Count -eq 0 -and $rightParts.Pre.Count -gt 0) { return 1 }
    if ($leftParts.Pre.Count -gt 0 -and $rightParts.Pre.Count -eq 0) { return -1 }
    $limit = [Math]::Max($leftParts.Pre.Count, $rightParts.Pre.Count)
    for ($i = 0; $i -lt $limit; $i++) {
        if ($i -ge $leftParts.Pre.Count) { return -1 }
        if ($i -ge $rightParts.Pre.Count) { return 1 }
        $leftNumber = 0L
        $rightNumber = 0L
        $leftNumeric = [int64]::TryParse($leftParts.Pre[$i], [ref]$leftNumber)
        $rightNumeric = [int64]::TryParse($rightParts.Pre[$i], [ref]$rightNumber)
        if ($leftNumeric -and $rightNumeric) {
            if ($leftNumber -lt $rightNumber) { return -1 }
            if ($leftNumber -gt $rightNumber) { return 1 }
        } elseif ($leftNumeric -ne $rightNumeric) {
            if ($leftNumeric) { return -1 }
            return 1
        } else {
            $comparison = [string]::CompareOrdinal($leftParts.Pre[$i], $rightParts.Pre[$i])
            if ($comparison -lt 0) { return -1 }
            if ($comparison -gt 0) { return 1 }
        }
    }
    return 0
}

$SourceVersion = Get-BinaryVersion $SourceBinary
if ($SourceVersion -notmatch '^v\d+\.\d+\.\d+') {
    throw "bundled binary does not contain a release version"
}

New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
$TargetBinary = Join-Path $BinDir "querysplunk.exe"
$CurrentVersion = $null
if (Test-Path -LiteralPath $TargetBinary -PathType Leaf) {
    try { $CurrentVersion = Get-BinaryVersion $TargetBinary } catch { $CurrentVersion = $null }
}
if ((Test-Path -LiteralPath $TargetBinary) -and -not $CurrentVersion) {
    throw "$TargetBinary exists but is not a recognized querysplunk installation; move it or choose -BinDir"
}
if ($CurrentVersion -and $CurrentVersion -ne $SourceVersion) {
    if (-not $Upgrade) {
        throw "querysplunk $CurrentVersion is installed; rerun with -Upgrade for $SourceVersion"
    }
    $comparison = Compare-SemVer $SourceVersion $CurrentVersion
    if ($comparison -eq -1 -and -not $AllowDowngrade) {
        throw "refusing to downgrade querysplunk from $CurrentVersion to $SourceVersion; use -Upgrade -AllowDowngrade to confirm"
    }
}

$suffix = "$PID.$([guid]::NewGuid().ToString('N'))"
$BinaryTemp = Join-Path $BinDir ".querysplunk.install.$suffix"
$BinaryBackup = Join-Path $BinDir ".querysplunk.backup.$suffix"
Copy-Item -LiteralPath $SourceBinary -Destination $BinaryTemp
try {
    if (Test-Path -LiteralPath $TargetBinary) {
        Move-Item -LiteralPath $TargetBinary -Destination $BinaryBackup
    }
    Move-Item -LiteralPath $BinaryTemp -Destination $TargetBinary
    $InstalledVersion = Get-BinaryVersion $TargetBinary
    if ($InstalledVersion -ne $SourceVersion) {
        throw "installed binary version did not match the release"
    }
    Remove-Item -LiteralPath $BinaryBackup -Force -ErrorAction SilentlyContinue
} catch {
    Remove-Item -LiteralPath $TargetBinary, $BinaryTemp -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $BinaryBackup) {
        Move-Item -LiteralPath $BinaryBackup -Destination $TargetBinary
    }
    throw "binary installation failed; the previous installation was restored"
}

function Install-AgentSkill([string]$Assistant) {
    if ($Assistant -eq "codex") {
        $target = Join-Path $HomeDir ".codex/skills/querysplunk"
    } elseif ($Assistant -eq "claude") {
        $target = Join-Path $HomeDir ".claude/skills/querysplunk"
    } else {
        throw "unsupported assistant target $Assistant"
    }
    $parent = Split-Path -Parent $target
    New-Item -ItemType Directory -Path $parent -Force | Out-Null
    $temp = Join-Path $parent ".querysplunk.install.$suffix.$Assistant"
    $backup = Join-Path $parent ".querysplunk.backup.$suffix.$Assistant"
    Remove-Item -LiteralPath $temp, $backup -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $temp | Out-Null
    Copy-Item -Path (Join-Path $SkillSource "*") -Destination $temp -Recurse -Force
    try {
        if (Test-Path -LiteralPath $target) {
            Move-Item -LiteralPath $target -Destination $backup
        }
        Move-Item -LiteralPath $temp -Destination $target
        if (-not (Test-Path -LiteralPath (Join-Path $target "SKILL.md") -PathType Leaf)) {
            throw "installed skill is incomplete"
        }
        Remove-Item -LiteralPath $backup -Recurse -Force -ErrorAction SilentlyContinue
    } catch {
        Remove-Item -LiteralPath $target, $temp -Recurse -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $backup) {
            Move-Item -LiteralPath $backup -Destination $target
        }
        throw "$Assistant skill installation failed; the previous skill was restored"
    }
    Write-Output "Installed $Assistant skill: $target"
}

if ($Agent -eq "auto") {
    $codexDetected = (Test-Path -LiteralPath (Join-Path $HomeDir ".codex")) -or ($null -ne (Get-Command codex -ErrorAction SilentlyContinue))
    $claudeDetected = (Test-Path -LiteralPath (Join-Path $HomeDir ".claude")) -or ($null -ne (Get-Command claude -ErrorAction SilentlyContinue))
    if ($codexDetected -and $claudeDetected) { $Agent = "both" }
    elseif ($codexDetected) { $Agent = "codex" }
    elseif ($claudeDetected) { $Agent = "claude" }
    else { $Agent = "none" }
}

switch ($Agent) {
    "codex" { Install-AgentSkill "codex" }
    "claude" { Install-AgentSkill "claude" }
    "both" { Install-AgentSkill "codex"; Install-AgentSkill "claude" }
    "none" { Write-Output "No assistant skill selected; use -Agent codex, claude, or both to install one." }
}

Write-Output "Installed querysplunk $SourceVersion`: $TargetBinary"
$pathEntries = $env:PATH -split [IO.Path]::PathSeparator
if ($BinDir -notin $pathEntries) {
    Write-Output "Add querysplunk to your user PATH with:"
    Write-Output "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + [IO.Path]::PathSeparator + '$BinDir', 'User')"
}
Write-Output "Verify with: & '$TargetBinary' -version"
Write-Output "This installer did not read or configure Splunk credentials."
