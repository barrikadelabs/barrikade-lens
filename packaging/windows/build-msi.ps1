[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][ValidateSet('x64', 'arm64')][string]$Architecture,
    [Parameter(Mandatory = $true)][string]$Binary,
    [Parameter(Mandatory = $true)][string]$Output
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = Resolve-Path (Join-Path $PSScriptRoot '../..')
$msiVersion = ([regex]::Match($Version, '^\d+\.\d+\.\d+')).Value
if (-not $msiVersion) { throw 'Version must begin with three numeric components' }
if (-not (Get-Command wix -ErrorAction SilentlyContinue)) { throw 'WiX 4.0.6 must be installed as the wix .NET tool' }

$outputDirectory = Split-Path -Parent $Output
if ($outputDirectory) { New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null }

& wix build (Join-Path $PSScriptRoot 'Product.wxs') `
    -arch $Architecture `
    -d "LensVersion=$msiVersion" `
    -d "LensBinary=$Binary" `
    -d "LicenseFile=$(Join-Path $repositoryRoot 'LICENSE')" `
    -d "NoticeFile=$(Join-Path $repositoryRoot 'NOTICE')" `
    -d "ThirdPartyNoticesFile=$(Join-Path $repositoryRoot 'THIRD_PARTY_NOTICES.md')" `
    -d "FullCleanupScript=$(Join-Path $PSScriptRoot 'full-cleanup.ps1')" `
    -o $Output
if ($LASTEXITCODE -ne 0) { throw "WiX failed with exit code $LASTEXITCODE" }
