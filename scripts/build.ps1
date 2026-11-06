<#
.SYNOPSIS
    Builds bin\seelegent.exe with the dashboard embedded (the same steps as `make build`).

.PARAMETER SkipWeb
    Reuse the current web\dist instead of rebuilding the dashboard.

.EXAMPLE
    pwsh -File scripts/build.ps1
#>
[CmdletBinding()]
param(
    [switch] $SkipWeb
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$module = 'github.com/NovaSeele/Seelegent'

function Invoke-Native {
    param([string] $Command, [string[]] $Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Get-GitValue {
    param([string[]] $Arguments, [string] $Default)
    # git writes to stderr when there is no tag; that is not an error here.
    $ErrorActionPreference = 'Continue'
    $value = & git -C $root @Arguments 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $value) { return $Default }
    return "$value".Trim()
}

Push-Location $root
try {
    if (-not $SkipWeb) {
        Push-Location (Join-Path $root 'web')
        try {
            Invoke-Native pnpm @('install', '--frozen-lockfile')
            Invoke-Native pnpm @('build')
        }
        finally {
            Pop-Location
        }
    }

    $version = Get-GitValue @('describe', '--tags', '--dirty') 'dev'
    $commit = Get-GitValue @('rev-parse', '--short=12', 'HEAD') 'unknown'
    $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    $ldflags = @(
        '-s', '-w',
        "-X $module/internal/buildinfo.version=$version",
        "-X $module/internal/buildinfo.commit=$commit",
        "-X $module/internal/buildinfo.date=$date"
    ) -join ' '

    $exe = if ($env:OS -eq 'Windows_NT' -or $IsWindows) { 'seelegent.exe' } else { 'seelegent' }
    $out = Join-Path 'bin' $exe
    Invoke-Native go @('build', '-trimpath', '-ldflags', $ldflags, '-o', $out, './cmd/seelegent')
    Write-Host "Built $out ($version, $commit)"
}
finally {
    Pop-Location
}
